// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package instance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/pkg/errors"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
)

var _ Instance = (*Cos)(nil)

type ProgressListener struct {
	Ctx     context.Context
	Log     log.Logger
	RevDone chan struct{}
}

type Cos struct {
	svc *s3.S3

	Region    string
	Url       string
	Bucket    string
	SecretID  string
	SecretKey string

	PartSize       int64
	MaxRetries     int
	ThreadPoolSize int
	Timeout        time.Duration
	TempDir        string

	Log log.Logger
}

func (c *Cos) newSession() (*session.Session, error) {
	creds := credentials.NewStaticCredentials(c.SecretID, c.SecretKey, "")
	config := &aws.Config{
		Region:      aws.String(c.Region),
		Endpoint:    &c.Url,
		Credentials: creds,
	}
	return session.NewSession(config)
}

func (c *Cos) Svc() *s3.S3 {
	if c.svc == nil {
		sess, _ := c.newSession()
		c.svc = s3.New(sess)
	}
	return c.svc
}

func (c *Cos) successLockPath(ctx context.Context, targetPath string) string {
	return path.Join(targetPath, successLock)
}

func (c *Cos) Exist(ctx context.Context, targetPath string) (bool, error) {
	_, err := c.Svc().HeadObject(&s3.HeadObjectInput{
		Bucket: aws.String(c.Bucket),
		Key:    aws.String(c.successLockPath(ctx, targetPath)),
	})
	return err == nil, nil
}

// 获取上传 id
func (c *Cos) initMultipartUpload(ctx context.Context, objectName, fileType string) (*s3.CreateMultipartUploadOutput, error) {
	resp, err := c.Svc().CreateMultipartUpload(
		&s3.CreateMultipartUploadInput{
			Bucket:      aws.String(c.Bucket),
			Key:         aws.String(objectName),
			ContentType: aws.String(fileType),
		},
	)
	return resp, err
}

func (c *Cos) uploadPart(
	ctx context.Context, resp *s3.CreateMultipartUploadOutput, fileBytes []byte, partNumber int64,
) (*s3.CompletedPart, error) {
	tryNum := 1
	partInput := &s3.UploadPartInput{
		Body:          bytes.NewReader(fileBytes),
		Bucket:        resp.Bucket,
		Key:           resp.Key,
		PartNumber:    aws.Int64(partNumber),
		UploadId:      resp.UploadId,
		ContentLength: aws.Int64(int64(len(fileBytes))),
	}

	for tryNum <= c.MaxRetries {
		uploadResult, err := c.Svc().UploadPart(partInput)
		if err != nil {
			if tryNum == c.MaxRetries {
				return nil, err
			}
			c.Log.Warnf(ctx, "Retrying to upload part %v", partNumber)
			tryNum++
		} else {
			c.Log.Debugf(ctx, "Uploaded part #%v size %d", partNumber, len(fileBytes))
			return &s3.CompletedPart{
				ETag: uploadResult.ETag, PartNumber: aws.Int64(partNumber),
			}, nil
		}
	}
	return nil, nil
}

func (c *Cos) abortMultipartUpload(ctx context.Context, resp *s3.CreateMultipartUploadOutput) error {
	c.Log.Infof(ctx, "Aborting multipart upload for UploadId#"+*resp.UploadId)
	abortInput := &s3.AbortMultipartUploadInput{
		Bucket:   resp.Bucket,
		Key:      resp.Key,
		UploadId: resp.UploadId,
	}
	_, err := c.Svc().AbortMultipartUpload(abortInput)
	return err
}

func (c *Cos) completeMultipartUpload(ctx context.Context, resp *s3.CreateMultipartUploadOutput, completedParts []*s3.CompletedPart) (*s3.CompleteMultipartUploadOutput, error) {
	completeInput := &s3.CompleteMultipartUploadInput{
		Bucket:   resp.Bucket,
		Key:      resp.Key,
		UploadId: resp.UploadId,
		MultipartUpload: &s3.CompletedMultipartUpload{
			Parts: completedParts,
		},
	}
	return c.Svc().CompleteMultipartUpload(completeInput)
}

func (c *Cos) multipartUpload(ctx context.Context, source, target string) error {
	// 打开本地文件
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open file : " + err.Error())
	}
	defer file.Close()

	fileInfo, _ := file.Stat()
	size := fileInfo.Size()

	partNum := int(math.Ceil(float64(size) / float64(c.PartSize)))

	var buffer []byte

	// size 小于 partSize 的直接上传处理
	if size < c.PartSize {
		buffer = make([]byte, size)
		file.Read(buffer)
		res, err := c.Svc().PutObject(&s3.PutObjectInput{
			Bucket: aws.String(c.Bucket),
			Key:    aws.String(target),
			Body:   bytes.NewReader(buffer),
		})
		if err != nil {
			return err
		}
		c.Log.Infof(ctx, "upload file with put object %s", res.String())
		return nil
	}

	c.Log.Infof(ctx, "created multipart upload request, size: %d, partSize: %d, partNum: %d", size, c.PartSize, partNum)

	buffer = make([]byte, c.PartSize)
	fileType := http.DetectContentType(buffer)
	// 建立分段上传连接
	resp, err := c.initMultipartUpload(ctx, target, fileType)
	if err != nil {
		return fmt.Errorf("InitMultipartUpload : " + err.Error())
	}

	var (
		completedParts = make([]*s3.CompletedPart, 0, partNum)
	)
	partNumber := int64(1)

	for {
		switch nr, err1 := file.Read(buffer[:]); true {
		case nr < 0:
			c.Log.Errorf(ctx, "cat: error reading: %s\n", err.Error())
			return err1
		case nr == 0:
			completeResponse, err2 := c.completeMultipartUpload(ctx, resp, completedParts)
			if err2 != nil {
				return err2
			}

			c.Log.Infof(ctx, "Successfully uploaded file: %s", *completeResponse.Location)
			return nil
		case nr > 0:
			completedPart, err2 := c.uploadPart(ctx, resp, buffer[0:nr], partNumber)
			if err2 != nil {
				c.Log.Errorf(ctx, err2.Error())
				// 终止分段上传能力
				err3 := c.abortMultipartUpload(ctx, resp)
				if err3 != nil {
					c.Log.Errorf(ctx, err3.Error())
				}
				// 保留原上传错误
				return err2
			}
			partNumber++
			completedParts = append(completedParts, completedPart)
		}
	}
}

// Upload cfs 直接拷贝
func (c *Cos) Upload(ctx context.Context, sourcePath, targetPath string) error {
	// 进入到拷贝流程的话，如果本地存在完成标记需要删除重新拷贝
	os.Remove(c.successLockPath(ctx, sourcePath))

	c.Log.Infof(ctx, "start upload : %s => %s", sourcePath, targetPath)

	// 遍历文件夹
	walkFunc := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			// 除了完成标记位都上传
			if path != c.successLockPath(ctx, sourcePath) {
				source := path
				target := strings.ReplaceAll(source, sourcePath, targetPath)
				err = c.multipartUpload(ctx, source, target)
				if err != nil {
					return fmt.Errorf("%s upload error %s", source, err.Error())
				}
			}
		}
		return nil
	}
	err := filepath.Walk(sourcePath, walkFunc)
	if err != nil {
		return err
	}

	// 全部上传完毕
	f, err := os.Create(c.successLockPath(ctx, sourcePath))
	if err != nil {
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}

	return c.multipartUpload(ctx, c.successLockPath(ctx, sourcePath), c.successLockPath(ctx, targetPath))
}

// Download 下载文件，因为 cfs 是直接挂在的所以直接返回即可
func (c *Cos) Download(ctx context.Context, sourcePath, targetDir string) (string, error) {
	var (
		fPath string
		span  oleltrace.Span
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "download")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("source-path", sourcePath, span)
	trace.InsertStringIntoSpan("target-dir", targetDir, span)

	c.Log.Infof(ctx, "start download: %s => %s", sourcePath, targetDir)

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(c.Bucket),
		Prefix: aws.String(sourcePath),
	}
	resp, err := c.Svc().ListObjectsV2(input)
	if err != nil {
		err = fmt.Errorf(err.Error())
		return fPath, err
	}

	_, err = os.Stat(targetDir)
	if errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(targetDir, os.ModePerm)
		if err != nil {
			return fPath, err
		}
	}

	for _, item := range resp.Contents {
		res, err := c.Svc().GetObject(&s3.GetObjectInput{
			Bucket: aws.String(c.Bucket),
			Key:    item.Key,
		})
		if err != nil {
			err = fmt.Errorf(err.Error())
			return fPath, err
		}
		// key 就是路径
		fPath := filepath.Join(targetDir, *item.Key)
		// 如果目录不存在则新建目录
		fDir, _ := filepath.Split(fPath)
		_, err = os.Stat(fDir)
		if errors.Is(err, os.ErrNotExist) {
			err := os.MkdirAll(fDir, os.ModePerm)
			if err != nil {
				return fPath, err
			}
		}

		// 创建文件并写入本地
		f, err := os.Create(fPath)
		if err != nil {
			return fPath, err
		}
		_, err = io.Copy(f, res.Body)
		f.Close()
		if err != nil {
			return fPath, err
		}
	}
	fPath = filepath.Join(targetDir, sourcePath)
	// 如果目录不存在则新建目录
	fDir, _ := filepath.Split(fPath)
	_, err = os.Stat(fDir)
	if errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(fDir, os.ModePerm)
		if err != nil {
			return fPath, err
		}
	}

	trace.InsertStringIntoSpan("target-path", fPath, span)

	return fPath, nil
}

// Delete 删除文件
func (c *Cos) Delete(ctx context.Context, targetPath string) error {
	return nil
}
