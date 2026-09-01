# cloudloupe에 기여하기

cloudloupe는 조회 전용 AWS 조사 도구다. 기능 추가보다 조회 전용 경계를 지키는 것이 우선이며,
모든 변경은 실제 AWS 자격증명 없이 검증할 수 있어야 한다.

설계 판단의 기준은 [Go 설계 원칙](.kiro/steering/go-conventions.md)이다. 이 문서는 작업 순서와
검증 명령을 설명하고, 설계 원칙을 대신하지 않는다.

## 준비

필수 도구:

- `go.mod`에 지정된 버전 이상의 Go
- Git
- Make

정적 분석을 실행하려면 프로젝트와 같은 버전의 golangci-lint가 필요하다.

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2
```

릴리스 구성을 로컬에서 시험할 때만 GoReleaser가 필요하다. GitHub Actions는 GoReleaser
`v2.18.0`을 사용한다.

```sh
go install github.com/goreleaser/goreleaser/v2@v2.18.0
```

## 개발 환경 만들기

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe
go mod download
make test
```

테스트는 실제 AWS를 호출하지 않는다. 로컬 AWS 자격증명을 테스트에 넣거나 네트워크 호출을
전제로 한 테스트를 추가하지 않는다.

## 일반적인 작업 순서

변경 중에는 관련 패키지 테스트를 먼저 실행한다.

```sh
go test ./internal/awsclient
go test ./internal/collector/elbv2
```

작업이 끝나면 전체 검사를 실행한다.

```sh
make ci
make tidy-check
make lint
make test-race
make cross
git diff --check
```

각 명령의 역할:

| 명령 | 검사 내용 |
| --- | --- |
| `make test` | 전체 단위 테스트 |
| `make ci` | gofmt, go vet, 조회 전용 가드 자체 검사, 조회 전용 검사, 테스트, 현재 플랫폼 빌드 |
| `make tidy-check` | `go.mod`와 `go.sum`이 정리된 상태인지 확인 |
| `make lint` | 프로젝트 설정으로 golangci-lint 실행 |
| `make test-race` | CGO를 이 검사에서만 켜고 데이터 레이스 검사 |
| `make cross` | macOS, Linux, Windows의 amd64/arm64 바이너리 빌드 |
| `make snapshot` | 태그와 게시 없이 GoReleaser 릴리스 산출물 생성 |
| `make verify-readonly` | 허용된 조회 AWS API만 호출하는지 검사 |
| `make help` | 사용할 수 있는 전체 Make 타깃 출력 |

`make ci`에는 외부 도구가 필요한 `make lint`와 비용이 큰 race/cross 검사가 포함되지 않는다.
PR 전에는 위 명령을 별도로 실행한다. macOS에서는 Windows 바이너리를 실행할 수 없으므로,
Windows 런타임 검증의 최종 근거는 GitHub Actions의 `windows-latest` 잡이다.

## 새 AWS 리소스 추가

새 리소스는 다음 순서로 추가한다.

1. `internal/model`에 안정적인 타입 ID와 필요한 도메인 값을 정의한다.
2. `internal/collector/<service>`에 수집기를 구현한다.
3. 수집기가 필요한 SDK 메서드만 담은 1~3개 메서드의 좁은 인터페이스를 정의한다.
4. SDK 포인터는 수집기 경계에서 Go 값으로 변환한다.
5. 실제 AWS를 호출하지 않는 fake로 수집기 테스트를 작성한다.
6. `internal/catalog`에 수집기를 명시적으로 등록한다. `init()` 등록은 사용하지 않는다.
7. 관계가 있으면 `model.Ref`를 만들고 `internal/graph`의 결정적 해석 결과를 검증한다.
8. README 지원 리소스 표와 `examples/iam`의 최소 권한 정책을 함께 확인한다.
9. `make verify-readonly`로 새 SDK 호출이 조회 allow-list 안에 있는지 확인한다.

AWS 쓰기 API를 테스트 목적으로라도 추가하지 않는다. 필요한 API 이름이 조회 동사 allow-list에
없다면 우회하지 말고 설계를 먼저 검토한다.

## 오류와 부분 실패

- I/O 함수는 `context.Context`를 첫 번째 인자로 받는다.
- 패키지 경계를 넘는 오류에는 문맥을 붙여 `%w`로 감싼다.
- 리전이나 리소스 하나의 실패 때문에 성공한 수집 결과를 버리지 않는다.
- 사용자 메시지와 원본 진단 오류를 분리한다.
- 문자열 비교로 AWS 오류를 판별하기 전에 `errors.Is`, `errors.As`, `smithy.APIError`를 사용한다.

## TUI 변경

- `View`에서는 I/O나 상태 변경을 하지 않는다.
- 부수효과는 `tea.Cmd`로 실행하고 결과를 `tea.Msg`로 받는다.
- 모델 필드를 다른 고루틴에서 직접 수정하지 않는다.
- 키 바인딩은 공용 `key.Binding`에 추가해 도움말과 실제 동작이 어긋나지 않게 한다.
- 테스트는 `Update(msg)` 호출 뒤 상태 전이를 검증하고, 전체 렌더링 문자열 비교는 피한다.

## PR 전 확인 목록

- [ ] `make ci`가 통과한다.
- [ ] `make tidy-check`가 통과한다.
- [ ] `make lint`가 통과한다.
- [ ] `make test-race`가 통과한다.
- [ ] `make cross`가 통과한다.
- [ ] `git diff --check`가 통과한다.
- [ ] 새 테스트가 실제 AWS나 사용자 자격증명을 사용하지 않는다.
- [ ] AWS 호출은 조회 API뿐이며 `make verify-readonly`를 우회하지 않았다.
- [ ] 실제 계정 ID, ARN, 자격증명, 리포트 데이터가 diff에 없다.
- [ ] 새 리소스라면 README 지원 표와 최소 IAM 정책을 확인했다.
- [ ] 사용자 출력과 주석은 한국어이며 식별자와 출력 계약은 영어를 유지한다.

커밋 제목은 Conventional Commit 접두사를 사용한다.

```text
feat: 새 수집기를 추가한다
fix: Windows 경로 처리를 수정한다
ci: 릴리스 검증을 보강한다
docs: 기여 절차를 보강한다
```

## 릴리스 변경

`.goreleaser.yaml`, `install.sh`, `.github/workflows/release.yml`을 변경했다면 다음도 실행한다.

```sh
sh -n install.sh
goreleaser check
make snapshot
```

태그 생성과 실제 게시 절차는 [RELEASING.md](RELEASING.md)를 따른다.
