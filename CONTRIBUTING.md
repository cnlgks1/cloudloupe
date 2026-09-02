# 기여하기

조회 전용 경계를 지키는 것이 기능 추가보다 우선입니다. 모든 변경은 실제 AWS 자격증명 없이
검증할 수 있어야 합니다. 설계 판단 기준은 [Go 설계 원칙](docs/go-conventions.md)이고, 이
문서는 작업 순서와 검증 명령만 다룹니다.

## 준비

Go(`go.mod`에 지정된 버전 이상), Git, Make와 POSIX 셸 도구가 필요합니다. `make test-race`를
실행하려면 현재 플랫폼의 C 컴파일러도 설치되어 있어야 합니다. Windows 런타임은 지원하지만
전체 Makefile 검증은 Git Bash나 WSL 같은 POSIX 환경에서 실행합니다.

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe
go mod download
make test
```

정적 분석과 릴리스 시험에는 CI와 같은 버전의 도구가 필요합니다.

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.2
go install github.com/goreleaser/goreleaser/v2@v2.18.0
```

## 검증

작업 중에는 관련 패키지만 돌립니다.

```sh
go test ./internal/collector/elbv2
```

PR 전에는 전체 검사를 돌립니다. `make ci`가 크로스 컴파일까지 포함하므로 남는 것은 외부 도구가
필요한 lint와 C 컴파일러가 필요한 race입니다.

```sh
make ci && make tidy-check && make lint && make test-race && git diff --check
```

| 명령 | 검사 내용 |
| --- | --- |
| `make ci` | gofmt, go vet, 조회 전용 가드와 자체 검사, 테스트, 빌드, 6종 크로스 컴파일 |
| `make tidy-check` | `go.mod`, `go.sum`이 정리된 상태인지 |
| `make lint` | golangci-lint (`.golangci.yml` 설정) |
| `make test-race` | `CGO_ENABLED=1`로 데이터 레이스 검사 |
| `make cross` | 6종 OS/아키텍처 빌드 (`make ci`에 포함) |
| `make verify-readonly` | 조회 API만 호출하는지 |
| `make snapshot` | 게시 없이 GoReleaser 산출물 생성 |

크로스 컴파일을 `make ci`에 넣은 이유는 CI가 이 검사를 태그와 수동 실행으로 미뤘기 때문입니다.
로컬은 빌드 캐시가 살아 있으면 몇 초로 끝나지만 CI는 매번 캐시가 비어 10분이 걸립니다. 의존성을
추가한 직후에는 캐시가 무효화되어 1분대로 늘어납니다.

테스트는 실제 AWS를 호출하지 않습니다. 자격증명이나 네트워크를 전제로 한 테스트는 추가하지
않습니다. Windows 런타임의 최종 근거는 GitHub Actions의 `windows-latest` 잡입니다.

## 새 AWS 리소스 추가

1. `internal/model`에 타입 ID와 도메인 값을 정의하고, `SortResources`의 출력 순서에 새 타입을
   넣습니다. 빼먹으면 모르는 타입으로 취급되어 결과 맨 뒤로 밀립니다.
2. `internal/collector/<service>`에 수집기를 구현합니다. SDK 호출은 메서드 1~3개의 좁은
   인터페이스로 받고, 포인터는 이 경계에서 값으로 변환합니다. 필드 이름은 SDK 구조체 필드
   이름을, 값은 API 값을 그대로 씁니다. 목록을 받은 뒤 항목마다 다시 조회해야 하면
   `collect.FanOut`으로 동시 호출 수에 상한을 둡니다.
3. fake로 수집기 테스트를 작성합니다.
4. `internal/catalog`에 명시적으로 등록합니다. `init()`은 쓰지 않습니다. 목록 열(`Columns`)은
   수집기가 만드는 `Fields` 키와 문자열이 정확히 같아야 하고(다르면 열은 보이는데 셀이 빕니다),
   타입이 둘 이상인 그룹은 `SummaryColumns`도 채워야 합니다.
5. 관계가 있으면 `model.Ref`를 만들고 `internal/graph` 해석 결과를 검증합니다.
6. README 지원 리소스 표와 `examples/iam`의 조회 전용 예제 정책을 갱신합니다.
7. `make verify-readonly`로 새 호출이 allow-list 안인지 확인합니다.

쓰기 API는 테스트 목적으로도 추가하지 않습니다. 필요한 API가 조회 동사 allow-list에 없으면
우회하지 말고 설계를 먼저 논의하세요.

## PR 전 확인

- [ ] 위 검증 명령이 모두 통과한다
- [ ] 새 테스트가 실제 AWS나 사용자 자격증명을 쓰지 않는다
- [ ] AWS 호출은 조회 API뿐이며 `make verify-readonly`를 우회하지 않았다
- [ ] 실제 계정 ID, ARN, 자격증명, AWS 리소스 데이터가 diff에 없다
- [ ] 새 리소스라면 README 표와 IAM 예제를 갱신했다
- [ ] TUI에 보이는 문자열은 영어(AWS 용어), 주석과 문서는 한국어다

커밋 제목은 Conventional Commit 접두사(`feat:`, `fix:`, `ci:`, `docs:`)를 씁니다.

## 릴리스 파일을 바꿨다면

`.goreleaser.yaml`, `install.sh`, `.github/workflows/release.yml`을 건드렸으면 추가로 실행합니다.

```sh
sh -n install.sh
go run github.com/goreleaser/goreleaser/v2@v2.18.0 check
make snapshot
```

태그와 게시 절차는 [RELEASING.md](RELEASING.md)에 있습니다.
