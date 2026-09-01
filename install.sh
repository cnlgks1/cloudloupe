#!/bin/sh

set -eu

repository="cnlgks1/cloudloupe"
requested_version="${CLOUDLOUPE_VERSION:-latest}"

fail() {
    printf 'cloudloupe 설치 실패: %s\n' "$*" >&2
    exit 1
}

# curl과 wget 중 이미 설치된 도구를 사용한다.
download() {
    url=$1
    output=$2

    if command -v curl >/dev/null 2>&1; then
        curl --fail --location --silent --show-error --output "$output" "$url"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget --quiet --output-document="$output" "$url"
        return
    fi

    fail "curl 또는 wget이 필요합니다."
}

case "$(uname -s)" in
    Darwin)
        os=darwin
        ;;
    Linux)
        # WSL도 Linux 커널을 보고하므로 같은 정적 바이너리를 사용한다.
        os=linux
        ;;
    *)
        fail "지원하지 않는 운영체제입니다: $(uname -s). Windows에서는 Release ZIP을 직접 받으세요."
        ;;
esac

case "$(uname -m)" in
    x86_64 | amd64)
        arch=amd64
        ;;
    arm64 | aarch64)
        arch=arm64
        ;;
    *)
        fail "지원하지 않는 아키텍처입니다: $(uname -m)."
        ;;
esac

if [ -n "${CLOUDLOUPE_INSTALL_DIR:-}" ]; then
    install_dir=$CLOUDLOUPE_INSTALL_DIR
elif [ -n "${HOME:-}" ]; then
    install_dir=$HOME/.local/bin
else
    fail "HOME이 비어 있습니다. CLOUDLOUPE_INSTALL_DIR을 지정하세요."
fi

if [ -n "${CLOUDLOUPE_RELEASE_URL:-}" ]; then
    # 릴리스 미러와 로컬 스모크 테스트에서 같은 검증 경로를 재사용한다.
    release_url=${CLOUDLOUPE_RELEASE_URL%/}
else
    case "$requested_version" in
        latest)
            release_url="https://github.com/${repository}/releases/latest/download"
            ;;
        v*)
            release_url="https://github.com/${repository}/releases/download/${requested_version}"
            ;;
        *)
            release_url="https://github.com/${repository}/releases/download/v${requested_version}"
            ;;
    esac
fi

archive="cloudloupe_${os}_${arch}.tar.gz"
checksums=checksums.txt

temp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t cloudloupe) || fail "임시 디렉터리를 만들 수 없습니다."
install_temp=
cleanup() {
    rm -rf "$temp_dir"
    if [ -n "$install_temp" ]; then
        rm -f "$install_temp"
    fi
}
trap cleanup 0
trap 'exit 1' 1 2 3 15

echo "cloudloupe ${requested_version} 다운로드 중 (${os}/${arch})..."
download "${release_url}/${archive}" "${temp_dir}/${archive}"
download "${release_url}/${checksums}" "${temp_dir}/${checksums}"

expected=$(awk -v name="$archive" '$2 == name { print $1; exit }' "${temp_dir}/${checksums}")
[ -n "$expected" ] || fail "${checksums}에서 ${archive}의 체크섬을 찾지 못했습니다."

if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${temp_dir}/${archive}" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${temp_dir}/${archive}" | awk '{ print $1 }')
else
    fail "체크섬 검증에 sha256sum 또는 shasum이 필요합니다."
fi

[ "$actual" = "$expected" ] || fail "SHA-256 체크섬이 일치하지 않습니다."
echo "SHA-256 체크섬 확인 완료."

tar -xzf "${temp_dir}/${archive}" -C "$temp_dir"
[ -f "${temp_dir}/cloudloupe" ] || fail "아카이브에 cloudloupe 바이너리가 없습니다."

mkdir -p "$install_dir"
install_temp=$(mktemp "${install_dir}/.cloudloupe.XXXXXX") || fail "설치 임시 파일을 만들 수 없습니다."
cp "${temp_dir}/cloudloupe" "$install_temp"
chmod 0755 "$install_temp"
mv -f "$install_temp" "${install_dir}/cloudloupe"
install_temp=

printf '설치 완료: %s/cloudloupe\n' "$install_dir"
"${install_dir}/cloudloupe" --version

case ":${PATH:-}:" in
    *":${install_dir}:"*)
        ;;
    *)
        printf '\nPATH에 %s가 없습니다. 셸 설정에 다음 줄을 추가하세요.\n' "$install_dir"
        printf '  export PATH="%s:$PATH"\n' "$install_dir"
        ;;
esac
