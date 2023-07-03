#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

set -e

if [[ ${APPNAME} == '' ]]; then
    echo 'Err: not define APPNAME'
    exit -1
fi

if [[ ${GOPATH} == '' ]]; then
    echo 'Err: not define GOPATH'
    exit -1
fi

if [[ ${LDFLAGS} == '' ]]; then
    echo 'Err: not define GOPATH'
    exit -1
fi

if [[ ! -d ${PROJECTDIR} ]];  then
    echo 'Err: can not found PROJECTDIR'
    exit 1
fi

if [[ ! -d ${SCIRPTDIR} ]];  then
    echo 'Err: can not found SCIRPTDIR'
    exit 1
fi

build() {
    echo '> building '${APPNAME}
    echo "building ${APPNAME} ${GOOS} ${GOARCH}"
	if [[ "${GOOS}" == windows ]];  then
		sh -c "${GOBUILD} -o ${OUTPUTDIR}/${APPNAME}_${GOARCH}.exe" || exit 1
	else
        sh -c "${GOBUILD} -o ${OUTPUTDIR}/${APPNAME}_${GOARCH}" || exit 1
    fi
}

hash() {
    echo '> hashing '$1
    md5sum $1 | cut -f 1 -d ' ' | tee $1.md5
}

compress() {
    echo '> compressing '${APPNAME}
    upx -9 ${OUTPUTDIR}/${APPNAME}_${GOARCH}
}

copy_script() {
    echo '> copy script'
    cp -r ${SCIRPTDIR}/${GOOS}/* ${OUTPUTDIR}
    find ${OUTPUTDIR} -name '*.sh' -exec chmod +x {} \;
}

copy_config() {
    echo '> copy config'
    cp ${PROJECTDIR}/${APPNAME}.yml ${OUTPUTDIR}/${APPNAME}_template.yml
}

package() {
    echo '> packaging'
    cp ${PROJECTDIR}/VERSION ${OUTPUTDIR}
    cd ${OUTPUTDIR}
	if [[ "${GOOS}" == windows ]];  then
	    name=${BUILDDIR}/${RELEASE_NAME}.exe
		rm -rf ${name}
		7za a -sfx7zCon_windows.sfx ${name} *
	else
        name=${BUILDDIR}/${RELEASE_NAME}.tar.gz
        rm -rf ${name}
        tar czf ${name} *
	fi
	cd ${PROJECTDIR}
}

# go build
cd ${PROJECTDIR}
echo ${PROJECTDIR}
mkdir -p ${BUILDDIR}
echo ${BUILDDIR}

if [[ "${RELEASE_NAME}" = "" ]]; then
    RELEASE_NAME_AUTO=1
fi

for GOOS in ${RELEASE_GOOS}
do
    export GOOS

    rm -rf ${OUTPUTDIR}
    mkdir -p ${OUTPUTDIR}

    for GOARCH in ${RELEASE_GOARCH}
    do
        export GOARCH
        if [[ "${RELEASE_NAME_AUTO}" = 1 ]]; then
            RELEASE_NAME=${APPNAME}-`expr ${VERSION} : '\([0-9]*\.[0-9]*\.[0-9]*\)'`-${GOOS}
        fi

        build || exit 1

        if [[ "$RELEASE_WITH_COMPRESS" = 1 ]]; then
            compress || exit 1
        fi
		if [[ "${GOOS}" == windows ]];  then
		    hash "${OUTPUTDIR}/${APPNAME}_${GOARCH}.exe" || exit 1
		else
            hash "${OUTPUTDIR}/${APPNAME}_${GOARCH}" || exit 1
	    fi
    done

    copy_script || exit 2
    copy_config || exit 2
    package || exit 3
	if [[ "${GOOS}" == windows ]];  then
	    hash "${BUILDDIR}/${RELEASE_NAME}.exe" || exit 3
	else
	    hash "${BUILDDIR}/${RELEASE_NAME}.tar.gz" || exit 3
    fi
done

echo '> done'
echo 'build success'
