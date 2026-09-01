# 릴리스 운영

관리자용 문서입니다. 릴리스 원본은 GitHub Releases이고, 설치 스크립트와 (연결 후) Homebrew는
같은 자산을 씁니다.

## 자동화 범위

`v*` 태그를 push하면 `.github/workflows/release.yml`이 전체 CI를 먼저 돌리고, 성공한 뒤에만
GoReleaser `v2.18.0`으로 게시합니다. 산출물은 6개 아카이브(Unix `tar.gz`, Windows `zip`)와
`checksums.txt`(SHA-256)이며, 각 아카이브에 `README.md`와 `LICENSE`가 들어가고 바이너리에는
버전·커밋·빌드 날짜가 주입됩니다. `GITHUB_TOKEN`은 GitHub가 자동 제공합니다.

아직 연결되지 않은 것: Homebrew Tap 게시, macOS 코드 서명과 공증, Winget·Scoop. Tap과 서명을
실제로 시험하기 전에는 `brew install`을 공개 설치 방법으로 안내하지 않습니다.

## 버전 규칙

[Semantic Versioning](https://semver.org/) 형식(`v0.1.0`)을 씁니다. 이미 공개한 태그는
이동하거나 재사용하지 않습니다.

## 태그 전 검증

최신 `main`의 깨끗한 워킹트리에서 시작합니다. `git status --short`는 아무것도 출력하지 않아야
합니다.

```sh
git switch main && git pull --ff-only && git status --short
make ci && make tidy-check && make lint && make test-race && make cross
```

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

실패한 커밋의 workflow를 재실행하면 같은 파일과 설정을 쓰므로, 원인을 고친 새 커밋을 push해
새 실행을 만듭니다.

이미 공개한 태그는 다른 커밋으로 옮기지 않습니다. 수정 커밋 후 patch를 올려 새 릴리스를
게시합니다(`v0.1.0` 문제 → `v0.1.1`).

자산이 일부만 올라갔으면 workflow 로그와 Release 상태를 먼저 확인합니다. 태그나 Release
삭제는 사용자에게 영향을 주므로 자동으로 하지 않고 명시적으로 결정합니다.

## Homebrew Tap 연결

Tap은 소스 저장소와 분리된 공개 저장소(`cnlgks1/homebrew-tap`, 기본 브랜치 `main`)로
운영합니다. 기본 `GITHUB_TOKEN`은 다른 저장소에 쓸 수 없어 별도 토큰이 필요합니다.

1. fine-grained PAT을 만든다. Resource owner `cnlgks1`, 대상 저장소는 `homebrew-tap` 하나,
   권한은 `Contents: Read and write`만, 만료 기간 설정.
2. cloudloupe 저장소의 `Settings → Secrets and variables → Actions`에 `TAP_GITHUB_TOKEN`으로
   저장한다. 저장소 파일이나 로그에 넣지 않는다.
3. `.goreleaser.yaml`에 cask 구성을 추가한다.
4. `.github/workflows/release.yml`의 GoReleaser 단계에 `TAP_GITHUB_TOKEN` 환경 변수를 넘긴다.

```yaml
homebrew_casks:
  - name: cloudloupe
    ids: [cloudloupe]
    binaries: [cloudloupe]
    repository:
      owner: cnlgks1
      name: homebrew-tap
      branch: main
      token: "{{ .Env.TAP_GITHUB_TOKEN }}"
    homepage: https://github.com/cnlgks1/cloudloupe
    description: 조회 전용 AWS 인프라 조사 TUI
    license: MIT
```

필드 이름과 기본 동작은 도구 버전을 올릴 때마다
[GoReleaser Homebrew 문서](https://goreleaser.com/customization/homebrew_casks/)와 대조합니다.
토큰 권한 원칙은
[GitHub PAT 문서](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)를
참고합니다.

macOS 서명·공증 정책은 Tap 연결보다 먼저 정합니다. 서명하지 않은 바이너리는 Gatekeeper에
막힐 수 있고, `xattr`로 검사를 우회하는 post-install hook은 해법으로 쓰지 않습니다. 준비되지
않았다면 Tap을 보류하고 Release와 설치 스크립트만 제공합니다.

연결 후에는 새 릴리스가 `homebrew-tap`의 `Casks/cloudloupe.rb`를 갱신하는지 확인하고, 깨끗한
macOS에서 설치와 제거를 시험한 뒤 README에 `brew` 명령을 추가합니다.

```sh
brew tap cnlgks1/tap
brew install --cask cnlgks1/tap/cloudloupe
cloudloupe --version
brew uninstall --cask cloudloupe
```

## 보안 원칙

- 쓰기 권한은 게시 잡에만 둔다.
- 외부 Action은 전체 커밋 SHA로 고정한다.
- Tap 토큰은 `homebrew-tap`의 Contents 쓰기만 허용한다.
- 토큰과 AWS 자격증명을 릴리스 자산에 넣지 않는다.
- 체크섬은 손상 검출용이며 코드 서명을 대신하지 않는다.
- 태그와 자산을 강제로 덮어쓰지 않는다.
