# 릴리스 운영

관리자용 문서입니다. 릴리스 원본은 GitHub Releases입니다. 설치 스크립트는 Release 바이너리를
사용하고, Homebrew Formula는 같은 태그의 소스를 빌드합니다.

## 자동화 범위

`v*` 태그를 push하면 `.github/workflows/release.yml`이 전체 CI를 먼저 돌리고, 성공한 뒤에만
GoReleaser `v2.18.0`으로 게시합니다. 산출물은 6개 아카이브(Unix `tar.gz`, Windows `zip`)와
`checksums.txt`(SHA-256)이며, 각 아카이브에 `README.md`와 `LICENSE`가 들어가고 바이너리에는
버전·커밋·커밋 날짜가 주입됩니다. `GITHUB_TOKEN`은 GitHub가 자동 제공합니다.

Homebrew Formula 게시는 자동화되지 않았습니다. 아래 절차로 수동 관리합니다.

버전은 [Semantic Versioning](https://semver.org/) 형식(`v0.1.0`)을 쓰고, 이미 공개한 태그는
이동하거나 재사용하지 않습니다.

## 태그 전 검증

최신 `main`의 깨끗한 워킹트리에서 시작합니다. `git status --short`는 아무것도 출력하지 않아야
합니다.

```sh
git switch main && git pull --ff-only && git status --short
make ci && make tidy-check && make lint && make test-race
```

`make ci`가 6종 크로스 컴파일까지 돌립니다. 태그를 붙이면 CI가 같은 검사를 한 번 더 하고,
성공한 뒤 GoReleaser가 배포 아카이브를 만듭니다.

CI와 같은 GoReleaser 버전으로 구성과 산출물을 확인합니다.

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.0 check
go run github.com/goreleaser/goreleaser/v2@v2.18.0 release --snapshot --clean
```

`dist/`에 6개 아카이브와 `checksums.txt`가 있는지, 현재 플랫폼 바이너리의 `--version` 출력이
맞는지 확인한 뒤 정리합니다. 아카이브 안쪽 디렉터리 이름은 도구 버전에 따라 달라집니다.

```sh
ls dist/*.tar.gz dist/*.zip dist/checksums.txt
./dist/cloudloupe_darwin_arm64*/cloudloupe --version
make clean
```

## 게시

```sh
git tag -a v0.1.0 -m "v0.1.0"
git show --stat v0.1.0
git push origin v0.1.0
```

게시 후 workflow 성공만 보지 말고 결과를 확인합니다.

- [ ] Release가 태그와 같은 버전으로 생성됐다
- [ ] 6개 아카이브와 `checksums.txt`가 모두 있고, 체크섬에 6개가 다 적혀 있다
- [ ] 아카이브에 바이너리, README, LICENSE가 있다
- [ ] 바이너리 `--version`이 태그와 커밋을 출력한다
- [ ] 설치 스크립트가 최신과 특정 버전 모두 동작한다

```sh
curl -fsSL https://raw.githubusercontent.com/cnlgks1/cloudloupe/main/install.sh | sh
CLOUDLOUPE_VERSION=v0.1.0 sh install.sh
```

## 실패 대응

태그에서 실패한 workflow를 재실행하면 같은 커밋과 설정을 사용합니다. 원인을 고친 새 커밋을
`main`에 반영한 뒤 patch 버전을 올린 새 태그로 릴리스를 게시합니다. 일반 `main` push만으로는
태그용 Release workflow가 시작되지 않습니다.

이미 공개한 태그는 다른 커밋으로 옮기지 않습니다(`v0.1.0` 문제 → 수정 커밋 → `v0.1.1`).

자산이 일부만 올라갔으면 workflow 로그와 Release 상태를 먼저 확인합니다. 태그나 Release
삭제는 사용자에게 영향을 주므로 자동으로 하지 않고 명시적으로 결정합니다.

## Homebrew Formula 게시

공개 태그의 소스를 사용자 환경에서 빌드하는 Formula로 제공합니다. 별도 저장소
`cnlgks1/homebrew-tap`의 `Formula/cloudloupe.rb`를 수동으로 관리합니다
([Tap 관리 문서](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)).

태그를 게시한 뒤 소스 아카이브의 SHA-256을 구해 Formula의 `url`, `sha256`, 버전에 반영합니다.
Go 빌드 인자는 [Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)의 `std_go_args`를 씁니다.

```sh
curl -L https://github.com/cnlgks1/cloudloupe/archive/refs/tags/v0.1.0.tar.gz \
  | shasum -a 256
```

```ruby
class Cloudloupe < Formula
  desc "Read-only AWS infrastructure investigation TUI"
  homepage "https://github.com/cnlgks1/cloudloupe"
  url "https://github.com/cnlgks1/cloudloupe/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "소스_아카이브의_SHA256"
  license "MIT"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "0"

    ldflags = "-s -w -X main.version=v#{version}"
    system "go", "build",
           *std_go_args(output: bin/"cloudloupe", ldflags:),
           "./cmd/cloudloupe"
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/cloudloupe --version")
  end
end
```

push에는 평소 쓰는 로컬 GitHub 인증만 필요합니다. 릴리스마다 URL과 SHA-256을 갱신합니다.
push한 뒤 깨끗한 환경에서 확인합니다.

```sh
brew tap cnlgks1/tap
brew audit --strict --online cnlgks1/tap/cloudloupe
brew install --build-from-source cnlgks1/tap/cloudloupe
brew test cnlgks1/tap/cloudloupe
cloudloupe --version
brew uninstall cloudloupe
```

검증이 끝나면 README의 공개 설치 방법으로 다음 명령을 안내합니다.

```sh
brew install cnlgks1/tap/cloudloupe
```

## 보안 원칙

- 쓰기 권한은 게시 잡에만 둔다.
- 외부 Action은 전체 커밋 SHA로 고정한다.
- AWS 자격증명을 릴리스 자산에 넣지 않는다.
- 체크섬으로 다운로드한 아카이브의 무결성을 검증한다.
- 태그와 자산을 강제로 덮어쓰지 않는다.
