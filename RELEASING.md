# 릴리스 운영

관리자용 문서입니다. 릴리스 원본은 GitHub Releases입니다. 설치 스크립트와 Homebrew Formula는
모두 같은 태그의 Release 바이너리를 사용합니다.

## 자동화 범위

`v*` 태그를 push하면 `.github/workflows/release.yml`이 전체 CI를 먼저 돌리고, 성공한 뒤에만
GoReleaser `v2.18.0`으로 게시합니다. 산출물은 6개 아카이브(Unix `tar.gz`, Windows `zip`)와
`checksums.txt`(SHA-256)이며, 각 아카이브에 `README.md`와 `LICENSE`가 들어가고 바이너리에는
버전·커밋·커밋 날짜가 주입됩니다. `GITHUB_TOKEN`은 GitHub가 자동 제공합니다.

Homebrew Formula도 같은 릴리스에서 자동 게시됩니다. GoReleaser가 `cnlgks1/homebrew-tap`에
`Formula/cloudloupe.rb`를 생성·push하며, 버전·URL·SHA-256을 매 릴리스마다 갱신합니다. 자세한
설정은 아래 [Homebrew Formula 자동 게시](#homebrew-formula-자동-게시)를 참고합니다.

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

## Homebrew Formula 자동 게시

`.goreleaser.yaml`의 `brews:` 설정으로, 릴리스 때 GoReleaser가 Release 바이너리를 가리키는
Formula를 만들어 `cnlgks1/homebrew-tap`의 `Formula/cloudloupe.rb`로 push합니다. 버전·URL·
SHA-256은 매 릴리스마다 자동 갱신되므로 손으로 만질 것이 없습니다.

tap은 별도 저장소라 기본 `GITHUB_TOKEN`으로는 push할 수 없다. 한 번만 준비하면 된다.

1. `cnlgks1/homebrew-tap` 저장소를 공개로 만든다(비어 있어도 된다).
2. tap 저장소에 쓰기 권한이 있는 개인 액세스 토큰(PAT)을 발급한다.
   - Fine-grained PAT면 `cnlgks1/homebrew-tap`의 Contents 권한을 Read and write로 준다.
3. 메인 저장소 `Settings → Secrets and variables → Actions`에 그 토큰을
   `HOMEBREW_TAP_GITHUB_TOKEN` 이름으로 등록한다.

`release.yml`이 이 시크릿을 GoReleaser에 넘긴다. 설정 이후 `v*` 태그를 push하면 Formula가
자동으로 갱신된다.

> 참고: GoReleaser v2에서 `brews:`(Formula)는 deprecated이며 후속 major에서 `homebrew_casks:`
> (Cask)로 옮겨야 한다. Cask는 macOS 서명·공증이 필요해 서명 없이는 "damaged" 경고가 뜨므로,
> 서명 준비 전까지는 Formula 방식을 유지한다. GoReleaser 버전을 `v2.18.0`으로 고정해 두어
> deprecated 경고만 남고 게시는 정상 동작한다.

릴리스 후 깨끗한 환경에서 확인한다.

```sh
brew tap cnlgks1/tap
brew install cnlgks1/tap/cloudloupe
cloudloupe --version
brew uninstall cloudloupe
```

## 보안 원칙

- 쓰기 권한은 게시 잡에만 둔다.
- 외부 Action은 전체 커밋 SHA로 고정한다.
- AWS 자격증명을 릴리스 자산에 넣지 않는다.
- 체크섬으로 다운로드한 아카이브의 무결성을 검증한다.
- 태그와 자산을 강제로 덮어쓰지 않는다.
