// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ai_agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// LLMClient 大语言模型客户端接口
type LLMClient interface {
	// ChatCompletion 聊天完成
	ChatCompletion(ctx context.Context, messages []Message) (*Completion, error)
	// Embedding 生成嵌入向量
	Embedding(ctx context.Context, text string) ([]float64, error)
}

// Message 消息结构
type Message struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

// Completion 完成响应
type Completion struct {
	Content string `json:"content"`
	Usage   Usage  `json:"usage"`
}

// Usage 使用情况
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// LLMConfig LLM配置
type LLMConfig struct {
	Provider    string            `json:"provider"` // openai, claude, local
	APIKey      string            `json:"api_key"`
	BaseURL     string            `json:"base_url"`
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature float64           `json:"temperature"`
	Timeout     time.Duration     `json:"timeout"`
	Headers     map[string]string `json:"headers"`
}

// OpenAIClient OpenAI客户端实现
type OpenAIClient struct {
	config     *LLMConfig
	httpClient *http.Client
}

// NewLLMClient 创建LLM客户端
func NewLLMClient(config *LLMConfig) (LLMClient, error) {
	switch config.Provider {
	case "openai":
		return NewOpenAIClient(config)
	case "claude":
		return NewClaudeClient(config)
	case "local":
		return NewLocalLLMClient(config)
	default:
		return nil, errors.Errorf("unsupported LLM provider: %s", config.Provider)
	}
}

// NewOpenAIClient 创建OpenAI客户端
func NewOpenAIClient(config *LLMConfig) (*OpenAIClient, error) {
	if config.APIKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}

	if config.Model == "" {
		config.Model = "gpt-3.5-turbo"
	}

	if config.MaxTokens == 0 {
		config.MaxTokens = 1000
	}

	if config.Temperature == 0 {
		config.Temperature = 0.7
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &OpenAIClient{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// ChatCompletion 实现聊天完成
func (c *OpenAIClient) ChatCompletion(ctx context.Context, messages []Message) (*Completion, error) {
	requestBody := map[string]any{
		"model":       c.config.Model,
		"messages":    messages,
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal request body")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/v1/chat/completions",
		strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	// 添加自定义头部
	for key, value := range c.config.Headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	if len(response.Choices) == 0 {
		return nil, errors.New("no response choices returned")
	}

	return &Completion{
		Content: response.Choices[0].Message.Content,
		Usage: Usage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      response.Usage.TotalTokens,
		},
	}, nil
}

// Embedding 实现嵌入向量生成
func (c *OpenAIClient) Embedding(ctx context.Context, text string) ([]float64, error) {
	requestBody := map[string]any{
		"input": text,
		"model": "text-embedding-ada-002",
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal request body")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/v1/embeddings",
		strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	if len(response.Data) == 0 {
		return nil, errors.New("no embedding data returned")
	}

	return response.Data[0].Embedding, nil
}

// ClaudeClient Claude客户端实现
type ClaudeClient struct {
	config     *LLMConfig
	httpClient *http.Client
}

// NewClaudeClient 创建Claude客户端
func NewClaudeClient(config *LLMConfig) (*ClaudeClient, error) {
	if config.APIKey == "" {
		return nil, errors.New("Claude API key is required")
	}

	if config.Model == "" {
		config.Model = "claude-3-sonnet-20240229"
	}

	if config.MaxTokens == 0 {
		config.MaxTokens = 1000
	}

	if config.Temperature == 0 {
		config.Temperature = 0.7
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &ClaudeClient{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// ChatCompletion 实现聊天完成
func (c *ClaudeClient) ChatCompletion(ctx context.Context, messages []Message) (*Completion, error) {
	// 将消息转换为Claude格式
	var claudeMessages []map[string]any
	for _, msg := range messages {
		claudeMsg := map[string]any{
			"role":    msg.Role,
			"content": msg.Content,
		}
		claudeMessages = append(claudeMessages, claudeMsg)
	}

	requestBody := map[string]any{
		"model":       c.config.Model,
		"messages":    claudeMessages,
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal request body")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/v1/messages",
		strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var response struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	if len(response.Content) == 0 {
		return nil, errors.New("no response content returned")
	}

	return &Completion{
		Content: response.Content[0].Text,
		Usage: Usage{
			PromptTokens:     response.Usage.InputTokens,
			CompletionTokens: response.Usage.OutputTokens,
			TotalTokens:      response.Usage.InputTokens + response.Usage.OutputTokens,
		},
	}, nil
}

// Embedding 实现嵌入向量生成
func (c *ClaudeClient) Embedding(ctx context.Context, text string) ([]float64, error) {
	// Claude API 不直接支持嵌入，这里返回错误
	return nil, errors.New("Claude API does not support embeddings directly")
}

// LocalLLMClient 本地LLM客户端实现
type LocalLLMClient struct {
	config     *LLMConfig
	httpClient *http.Client
}

// NewLocalLLMClient 创建本地LLM客户端
func NewLocalLLMClient(config *LLMConfig) (*LocalLLMClient, error) {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:11434" // Ollama默认地址
	}

	if config.Model == "" {
		config.Model = "llama2"
	}

	if config.MaxTokens == 0 {
		config.MaxTokens = 1000
	}

	if config.Temperature == 0 {
		config.Temperature = 0.7
	}

	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second // 本地模型可能需要更长时间
	}

	return &LocalLLMClient{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// ChatCompletion 实现聊天完成
func (c *LocalLLMClient) ChatCompletion(ctx context.Context, messages []Message) (*Completion, error) {
	// 将消息合并为单个提示
	var prompt strings.Builder
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			prompt.WriteString("System: " + msg.Content + "\n\n")
		case "user":
			prompt.WriteString("User: " + msg.Content + "\n\n")
		case "assistant":
			prompt.WriteString("Assistant: " + msg.Content + "\n\n")
		}
	}
	prompt.WriteString("Assistant: ")

	requestBody := map[string]any{
		"model":  c.config.Model,
		"prompt": prompt.String(),
		"stream": false,
		"options": map[string]any{
			"temperature": c.config.Temperature,
			"num_predict": c.config.MaxTokens,
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal request body")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/api/generate",
		strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var response struct {
		Response string `json:"response"`
		Done     bool   `json:"done"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	return &Completion{
		Content: response.Response,
		Usage: Usage{
			PromptTokens:     len(strings.Split(prompt.String(), " ")),
			CompletionTokens: len(strings.Split(response.Response, " ")),
			TotalTokens:      len(strings.Split(prompt.String(), " ")) + len(strings.Split(response.Response, " ")),
		},
	}, nil
}

// Embedding 实现嵌入向量生成
func (c *LocalLLMClient) Embedding(ctx context.Context, text string) ([]float64, error) {
	// 本地LLM可能不支持嵌入，这里返回错误
	return nil, errors.New("local LLM client does not support embeddings")
}
