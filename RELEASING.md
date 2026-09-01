# cloudloupe 릴리스 운영

이 문서는 관리자가 cloudloupe 버전을 만들고 GitHub Release를 검증하는 절차를 설명한다.
릴리스 원본은 GitHub Releases이며, Homebrew와 설치 스크립트는 같은 릴리스 자산을 사용한다.

## 현재 상태

현재 자동화된 범위:

- `v*` 태그 push 시 전체 CI 실행
- CI 성공 후에만 GoReleaser 실행
- macOS, Linux, Windows의 amd64/arm64 바이너리 빌드
- Unix 계열 `tar.gz`, Windows `zip` 생성
- 각 아카이브에 `README.md`와 `LICENSE` 포함
- `checksums.txt`에 SHA-256 기록
- 버전, 커밋, 빌드 날짜를 바이너리에 주입

아직 연결되지 않은 범위:

- Homebrew Tap 게시
- macOS 코드 서명과 공증
- Winget, Scoop 등 Windows 패키지 저장소

Homebrew 설치 명령은 Tap과 서명 정책을 실제로 연결하고 시험하기 전에는 공개 설치 방법으로
안내하지 않는다.

## 버전 규칙

릴리스 태그는 [Semantic Versioning](https://semver.org/) 형식으로 만든다.

```text
v0.1.0
v0.1.1
v0.2.0
v1.0.0
```

- 호환되는 버그 수정: patch 증가
- 하위 호환 기능 추가: minor 증가
- 안정화 이후 호환되지 않는 변경: major 증가
- 이미 공개한 태그는 이동하거나 재사용하지 않는다.

## 태그 전 검증

릴리스는 최신 `main`의 깨끗한 워킹트리에서 시작한다.

```sh
git switch main
git pull --ff-only
git status --short
make ci
make tidy-check
make lint
make test-race
make cross
```

`git status --short`에는 아무것도 출력되지 않아야 한다.

GitHub Actions가 사용하는 정확한 GoReleaser 버전으로 구성과 snapshot을 확인한다.

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.0 check
go run github.com/goreleaser/goreleaser/v2@v2.18.0 release --snapshot --clean
```

`dist/`에서 다음 자산을 확인한다.

```text
cloudloupe_darwin_amd64.tar.gz
cloudloupe_darwin_arm64.tar.gz
cloudloupe_linux_amd64.tar.gz
cloudloupe_linux_arm64.tar.gz
cloudloupe_windows_amd64.zip
cloudloupe_windows_arm64.zip
checksums.txt
```

현재 플랫폼 바이너리의 버전 출력도 확인한다.

```sh
./dist/cloudloupe_darwin_arm64_v8.0/cloudloupe --version
```

위 내부 디렉터리 이름은 Go 또는 GoReleaser 버전에 따라 달라질 수 있다. 현재 플랫폼에 맞는
디렉터리를 선택한다. 검증이 끝나면 생성물을 정리한다.

```sh
make clean
```

## 태그와 GitHub Release 게시

예를 들어 `v0.1.0`을 게시한다.

```sh
git tag -a v0.1.0 -m "v0.1.0"
git show --stat v0.1.0
git push origin v0.1.0
```

태그 push 후 `.github/workflows/release.yml`이 다음 순서로 실행된다.

1. 재사용 가능한 CI workflow의 모든 잡을 실행한다.
2. Linux, Windows, 태그용 macOS 테스트가 모두 성공할 때까지 기다린다.
3. GoReleaser `v2.18.0`으로 6개 아카이브와 체크섬을 만든다.
4. GitHub의 해당 태그 Release에 자산을 게시한다.

일반 `GITHUB_TOKEN`은 GitHub가 자동으로 제공한다. 값을 직접 만들거나 저장소 파일에 넣지
않는다.

## 게시 후 확인

GitHub Actions의 Release workflow가 성공했는지만 보지 말고 실제 결과를 확인한다.

- [ ] GitHub Release가 태그와 같은 버전으로 생성됐다.
- [ ] 계획한 6개 플랫폼 아카이브가 모두 있다.
- [ ] `checksums.txt`에 6개 아카이브가 모두 있다.
- [ ] 아카이브에 바이너리, README, LICENSE가 있다.
- [ ] macOS 또는 Linux 바이너리의 `--version`이 태그와 커밋을 출력한다.
- [ ] 공개 저장소에서 최신 설치 스크립트가 정상 동작한다.
- [ ] 특정 버전 설치가 정상 동작한다.

```sh
curl -fsSL https://raw.githubusercontent.com/cnlgks1/cloudloupe/main/install.sh | sh

curl -fsSLO https://raw.githubusercontent.com/cnlgks1/cloudloupe/v0.1.0/install.sh
CLOUDLOUPE_VERSION=v0.1.0 sh install.sh
```

## 릴리스 실패 대응

과거 workflow의 빨간 기록은 수정 후에도 남는다. 실패한 커밋의 workflow를 다시 실행하면 같은
파일과 설정을 사용하므로, 원인을 고친 새 커밋을 push해 새 실행을 만든다.

이미 공개한 태그나 Release에 문제가 있으면 태그를 다른 커밋으로 강제로 이동하지 않는다.
수정 커밋을 만들고 patch 버전을 증가시켜 새 릴리스를 게시한다.

```text
v0.1.0 문제 발견
→ 수정 커밋
→ v0.1.1 게시
```

릴리스 자산 일부만 올라간 경우 먼저 workflow 로그와 GitHub Release 상태를 확인한다. 같은
태그를 반복 게시하기 전에 불완전한 Release와 자산을 정리할지 명시적으로 결정한다. 태그 삭제나
Release 삭제는 사용자에게 영향을 줄 수 있으므로 자동으로 수행하지 않는다.

## Homebrew Tap 연결

Homebrew Tap은 cloudloupe 소스 저장소와 분리된 작은 공개 저장소로 운영한다.

1. `cnlgks1/homebrew-tap` 공개 저장소를 만들고 기본 브랜치를 `main`으로 둔다.
2. GitHub fine-grained personal access token을 만든다.
3. Resource owner는 `cnlgks1`, repository access는 `homebrew-tap` 하나만 선택한다.
4. Repository permissions에서 `Contents: Read and write`만 부여한다.
5. 만료 기간을 설정하고 토큰을 주기적으로 교체한다.
6. cloudloupe 저장소의 `Settings → Secrets and variables → Actions`에
   `TAP_GITHUB_TOKEN` 이름으로 저장한다.

토큰은 저장소 파일, 로그, 명령 기록에 직접 넣지 않는다. 기본 `GITHUB_TOKEN`은 다른 저장소인
`homebrew-tap`에 쓸 수 없으므로 별도 토큰이 필요하다. 자세한 권한 원칙은
[GitHub 개인 액세스 토큰 문서](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)를 확인한다.

Tap을 연결할 때 `.goreleaser.yaml`에 다음 구성을 추가한다.

```yaml
homebrew_casks:
  - name: cloudloupe
    ids:
      - cloudloupe
    binaries:
      - cloudloupe
    repository:
      owner: cnlgks1
      name: homebrew-tap
      branch: main
      token: "{{ .Env.TAP_GITHUB_TOKEN }}"
    homepage: https://github.com/cnlgks1/cloudloupe
    description: 조회 전용 AWS 인프라 조사 TUI
    license: MIT
```

`.github/workflows/release.yml`의 GoReleaser 단계에는 secret을 환경 변수로 전달한다.

```yaml
env:
  GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}
```

GoReleaser의 현재 Homebrew Cask 설정은
[공식 Homebrew Casks 문서](https://www.goreleaser.com/customization/homebrew/)와 대조한다.
도구 버전을 올릴 때는 필드 이름과 기본 동작이 바뀌지 않았는지 다시 확인한다.

### macOS 서명과 공증

Homebrew Cask로 배포하는 macOS 바이너리는 코드 서명과 공증 정책을 먼저 결정한다. 서명하지
않은 바이너리는 Gatekeeper 격리 때문에 실행이 막힐 수 있다. `xattr`로 보안 검사를 우회하는
post-install hook을 기본 해법으로 사용하지 않는다. 서명과 공증을 준비하지 않았다면 Homebrew
Tap 연결을 보류하고 GitHub Release와 설치 스크립트를 우선 제공한다.

### Tap 연결 후 검증

새 릴리스가 `homebrew-tap`의 `Casks/cloudloupe.rb`를 갱신하는지 확인한 뒤 깨끗한 macOS
환경에서 설치와 제거를 시험한다.

```sh
brew tap cnlgks1/tap
brew install --cask cnlgks1/tap/cloudloupe
cloudloupe --version
brew uninstall --cask cloudloupe
```

검증이 끝난 뒤 README에 Homebrew 명령을 공개 설치 방법으로 추가한다.

## 보안 원칙

- 릴리스 workflow의 쓰기 권한은 게시 잡에만 둔다.
- 외부 GitHub Action은 검증한 전체 커밋 SHA로 고정한다.
- Tap 토큰은 `homebrew-tap`의 Contents 쓰기만 허용한다.
- 토큰과 AWS 자격증명을 릴리스 자산에 포함하지 않는다.
- 체크섬은 손상과 예상하지 못한 파일 변경을 검출하지만 코드 서명을 대신하지 않는다.
- 릴리스 태그와 자산을 강제로 덮어쓰지 않는다.
