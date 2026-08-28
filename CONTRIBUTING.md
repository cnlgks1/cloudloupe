# cloudloupe에 기여하기

관심 감사합니다. 이 문서는 개발 환경을 준비하는 방법과 리뷰에서 무엇을 보는지 설명합니다.

## 타협하지 않는 한 가지

**cloudloupe는 조회 전용입니다.** 변경 계열 AWS API 호출을 도입하는 기여는 기능이 아무리
유용하더라도 거부됩니다.

강제 방식은 allow-list입니다. SDK 호출은 `Describe`, `List`, `Get`, `Lookup`, `Search`,
`BatchGet` 중 하나로 시작해야 합니다. 푸시 전에 로컬에서 확인하세요.

```sh
make verify-readonly
```

이 접두사에 맞지 않는데 실제로는 조회인 API가 필요하다면(이름이 어색한 조회 API가 실제로
있습니다), `scripts/verify-readonly.sh`의 예외 목록에 **같은 커밋에서** 추가하고 왜 그것이
조회인지 주석으로 남기세요. 그래야 예외가 리뷰 대상이 됩니다.

## 개발 환경

필요한 것: Go 1.24.2 이상. 그 외에는 없습니다. CGO도 시스템 라이브러리도 필요 없습니다.

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe

make build            # ./cloudloupe 빌드
make test             # go test ./...
make lint             # golangci-lint
make verify-readonly  # 조회 전용 가드
make run              # 빌드해서 TUI 실행
make ci               # CI와 같은 검사를 한 번에
```

`make lint`는 [golangci-lint](https://golangci-lint.run/welcome/install/)가 필요합니다.

태그를 만들지 않고 릴리스 파이프라인을 시험해보려면:

```sh
make snapshot         # goreleaser release --snapshot --clean
```

전체 목록은 `make help`로 볼 수 있습니다.

## 언어 규칙

주석과 문서는 **한국어로** 씁니다. README와 에러 메시지, CLI 출력도 마찬가지입니다.

다음은 **영어로 유지합니다.** 바꾸면 깨지거나 관례를 벗어나는 것들입니다.

- 모든 식별자 — 패키지, 타입, 함수, 변수 이름
- 리소스 타입 ID(`ec2:instance`), 관계 이름(`attached-eni`), JSON 키(`accountId`),
  CSV 헤더 → 이건 출력 계약입니다. 번역하면 이 출력을 파싱하는 쪽이 깨집니다.
- `Test`로 시작하는 테스트 함수 이름
- Conventional Commit 접두사(`feat:`, `fix:`)와 AWS API 이름, 에러 코드

Go doc 주석은 식별자 이름으로 시작하는 관례를 한국어에서도 지킵니다
(`// Resource는 ...`). `revive`와 `godot`이 이를 검사합니다.

## 설계 규약

코드를 쓰기 전에 [`.kiro/steering/go-conventions.md`](.kiro/steering/go-conventions.md)를
읽어주세요. 이 코드베이스를 일관되게 유지하는 결정들이 적혀 있습니다. 리뷰에서 가장 자주
나오는 것들은 다음과 같습니다.

- **의존성은 한 방향으로만 흐릅니다.** `internal/model`은 외부 의존성이 0입니다. 도메인
  패키지는 `internal/tui`를 절대 import하지 않습니다.
- **부분 실패는 전체 실패가 아닙니다.** 한 리전의 권한 에러가 멀티 리전 수집 전체를
  중단시켜서는 안 됩니다. 결과와 에러를 *함께* 반환하세요.
- **Bubble Tea 규율.** `View`는 렌더링만 합니다. 다른 고루틴에서 모델 필드를 절대 만지지
  마세요. 백그라운드 작업은 `tea.Cmd`로 나가서 `tea.Msg`로 돌아옵니다.
- **표시 순서가 있는 데이터에 `map`을 쓰지 않습니다.** Go의 map 순회 순서는 무작위라
  상세 뷰 필드가 렌더링마다 뒤섞입니다. `[]model.Field`를 쓰세요.
- **동시 실행에 상한을 둡니다.** 리전 × 리소스 타입은 곱셈으로 늘어납니다. 팬아웃에 제한이
  없으면 API 스로틀링에 걸립니다.

## 수집기 추가하기

수집기는 주된 확장 지점이고, 의도적으로 지루하게 만들어두었습니다.

1. `internal/collect/<서비스>_<리소스>.go`를 만듭니다.
2. 호출하는 SDK 메서드만 담은 좁은 API 인터페이스를 정의합니다. 이것이 자격증명 없이
   수집기를 테스트할 수 있게 하는 지점입니다.
3. `Collect(ctx, Request) ([]model.Resource, error)`를 구현합니다. SDK 페이지네이터를
   쓰세요. 토큰 루프를 직접 돌리지 마세요.
4. 3단계 관계 그래프가 간선을 연결할 수 있도록 `Related` ref를 채웁니다.
5. `DefaultRegistry()`에 등록합니다. `init()` 부수효과를 쓰지 마세요.
6. fake API를 쓰는 테이블 드리븐 테스트를 추가합니다. 실제 AWS를 호출하는 테스트는
   허용하지 않습니다.

## 커밋과 PR

- Conventional Commit 접두사를 써주면 좋습니다: `feat:`, `fix:`, `docs:`, `refactor:`,
  `test:`, `chore:`, `ci:`. 체인지로그가 커밋 메시지에서 생성됩니다. 접두사는 영어로,
  본문은 한국어로 씁니다.
- PR 하나에 논리적 변경 하나. 수집기 추가와 무관한 리팩터링은 PR 두 개입니다.
- CI는 Linux, macOS, Windows에서 통과해야 합니다. Windows는 선택이 아닙니다. 거기서의
  터미널 렌더링 차이는 예외 상황이 아니라 지원 대상입니다.
- TUI 외형을 바꿨다면 PR 설명에 스크린샷이나 asciinema 녹화를 넣어주세요.

## 버그 신고

AWS 응답과 관련된 내용이라면 다음을 포함해주세요.

- cloudloupe 버전 (`cloudloupe --version`)
- OS, 아키텍처, 터미널 에뮬레이터
- 문제가 발생한 리전과 리소스 타입
- TUI에 표시된 에러 메시지

**올리기 전에 가려주세요.** 계정 ID, ARN, 사설 IP, DNS 이름은 실제 환경에 대한 인프라
정보입니다. 자리표시자로 바꿔주세요.
