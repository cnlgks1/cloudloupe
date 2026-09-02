# Go 설계 원칙

cloudloupe의 코드 리뷰 기준입니다. 원칙이 서로 충돌하면 1번(조회 전용)이 이깁니다.

## 1. 조회 전용 (최우선)

- 수집기 인터페이스의 메서드는 `Type()`과 `Collect()`뿐입니다. 쓰기 경로가 없습니다.
- SDK 호출은 allow-list로 제한합니다. 허용 접두사는 `Describe`, `List`, `Get`, `Lookup`,
  `Search`, `BatchGet`이고, 나머지는 `scripts/verify-readonly.sh`가 CI에서 실패시킵니다.
  deny-list는 AWS에 새 쓰기 API가 생기면 뚫립니다.
- `~/.aws/*`는 읽기만 합니다.
- 현재 실행 경로는 로컬 파일을 쓰지 않습니다.

```go
// 여기에 Apply/Delete/Modify가 생기면 설계 위반이다.
type Collector interface {
    Type() string
    Collect(ctx context.Context, req Request) ([]model.Resource, error)
}
```

## 2. 의존성은 한 방향으로 흐른다

```text
cmd/cloudloupe
├─ internal/tui
├─ internal/app → internal/awsclient, internal/catalog, internal/collect, internal/model
└─ internal/catalog → internal/collector/* → AWS SDK
internal/collector/* → internal/collect, internal/model
internal/collect, internal/graph → internal/model
```

- `internal/model`은 표준 라이브러리만 import합니다. `graph`나 앞으로 추가될 `findings`,
  `report`가 타입 하나 때문에 AWS SDK를 전이 의존하면 안 됩니다.
- 도메인 패키지는 `tui`를 import하지 않습니다. 헤드리스에서 단독 동작해야 합니다.
- 순환이 생기면 공통 타입을 아래 계층으로 내립니다. 인터페이스로 우회하지 않습니다.

## 3. 인터페이스는 쓰는 쪽에서 작게

구조체를 반환하고 인터페이스를 받습니다. SDK 클라이언트 전체를 목킹하지 말고 필요한 호출만
자릅니다. 자격증명 없는 단위 테스트가 여기서 나옵니다.

```go
type instancesAPI interface {
    DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}
```

- 메서드는 1~3개를 목표로 합니다. 커지면 책임이 섞인 신호입니다.
- 두 번째 구현이나 테스트 대역이 필요할 때 만듭니다. "나중에 대비"로는 만들지 않습니다.

## 4. context는 첫 인자

- I/O 하는 함수는 `ctx context.Context`를 첫 인자로 받고, 구조체 필드에 담지 않습니다.
- 타임아웃은 최상위 호출자(`cmd`)가 정합니다. 라이브러리 계층이 데드라인을 걸지 않습니다.
- TUI의 수집은 `esc`로 취소할 수 있어야 합니다.

## 5. 부분 실패는 전체 실패가 아니다

멀티 계정·멀티 리전 조회에서 가장 중요한 에러 설계입니다.

- 결과와 에러를 함께 반환합니다. `collect.Result`는 `Resources`, `Errors`, `Canceled`를
  담아 성공한 리전 데이터를 살립니다.
- 에러는 문맥을 붙여 감쌉니다: `fmt.Errorf("describe instances (%s/%s): %w", profile, region, err)`
- 판별은 `errors.Is`, `errors.As`로 하고 AWS 코드는 `smithy.APIError`에서 꺼냅니다.
  문자열 매칭은 최후 수단입니다.
- 사용자 메시지와 진단 메시지를 분리합니다. `awsclient.Explain`이 자격증명 없음, 권한 부족,
  토큰 만료, 리전 미지원을 사람이 읽을 문장으로 바꾸고 원본은 상세 뷰에 남깁니다.

## 6. 고루틴은 상한을 두고 호출자가 소유한다

- 팬아웃은 `errgroup.WithContext` + `SetLimit`이나 세마포어로 동시 실행 수를 제한합니다.
  리전 × 타입은 곱셈으로 늘어나고, 무제한 고루틴은 스로틀링을 부릅니다.
- 함수가 만든 고루틴은 그 함수가 리턴하기 전에 정리됩니다.
- 채널 파이프라인보다 뮤텍스로 보호한 슬라이스가 단순하면 그쪽을 씁니다.
- 모든 수집 고루틴이 끝난 뒤 완성된 `collect.Result`를 반환합니다.

## 7. Bubble Tea는 Elm 규율을 지킨다

- `Model`은 상태, `Update`는 전이, `View`는 순수 렌더링입니다. `View`에서 I/O나 상태 변경은
  금지입니다.
- 부수효과는 `tea.Cmd`로 내보내고 결과는 `tea.Msg`로 받습니다.
- 모델 필드를 다른 고루틴에서 만지지 않습니다. 백그라운드 수집은 `tea.Cmd` → `collectDoneMsg`
  경로만 씁니다.
- 화면 전환은 상태 enum + `switch`. 화면 로직이 커지면 자체 `Update`/`View`를 가진 서브모델로
  분리합니다.
- 키 바인딩은 `key.Binding`으로 모아 help 뷰와 공유합니다.
- ASCII 분기는 렌더링 코드에 넣지 않고 테마가 글리프를 들고 있게 합니다.

## 8. 렌더링은 결정적으로

- 표시 순서가 의미 있는 데이터에 `map`을 쓰지 않습니다. Go의 map 순회는 무작위라 상세 뷰
  필드 순서가 매번 바뀝니다. `[]Field{Key, Value}`를 씁니다.
- 시간은 `time.Time`(UTC)으로 보관하고 표시 직전에 포맷합니다.
- SDK 포인터는 경계에서 값으로 바꿉니다(`aws.ToString`, `aws.ToInt32`). 포인터가 도메인
  모델까지 들어오면 nil 체크가 전염됩니다.
- zero value가 의미 있게 동작하게 설계합니다.

## 9. 조립은 명시적으로, 전역 상태는 없다

- 레지스트리를 `init()` 부수효과로 채우지 않고 `internal/catalog` 한 곳에서 조립합니다.
  import 부수효과에 의존한 등록은 추적과 테스트 격리가 어렵습니다.
- 패키지 수준 가변 전역 변수를 두지 않습니다. 예외는 ldflags로 주입되는 `version`,
  `commit`, `date`입니다.
- 의존성은 생성자 인자로 주입합니다.

## 10. 명명

- 패키지명은 짧은 소문자 단수. `util`, `common`, `helpers`, `base`는 금지입니다.
- 스터터링 회피: `collect.Collector` (o), `collect.CollectCollector` (x)
- 라이브러리 코드는 `internal/` 아래 두고 필요한 것만 export합니다.
- 생성자는 `New`, `NewXxx`. 인자가 많아지면 functional options.
- getter에 `Get`을 붙이지 않습니다. `r.Name()`이지 `r.GetName()`이 아닙니다.

## 11. 테스트는 자격증명 없이 돌아간다

- AWS 호출은 좁은 인터페이스 경계에서 fake로 대체합니다. 테스트는 실제 AWS를 때리지 않습니다.
- 순수 로직은 테이블 드리븐 테스트로 검증합니다.
- TUI는 `Update(msg)`를 직접 호출해 "메시지 입력 → 기대 상태"로 테스트합니다. 렌더링 문자열
  비교는 깨지기 쉽습니다.

## 12. 크로스 플랫폼과 의존성 최소주의

- `CGO_ENABLED=0`과 순수 Go 의존만 유지합니다. 단일 정적 바이너리가 배포 전략의 전제입니다.
- 경로는 `filepath`, 홈은 `os.UserHomeDir()`. 구분자를 하드코딩하지 않습니다.
- OS 분기는 `runtime.GOOS` 산재보다 `_windows.go`, `_unix.go` 파일 분리를 우선합니다.
- 새 의존성 전에 표준 라이브러리나 기존 의존성으로 되는지 확인합니다. 의존성 트리는 그대로
  공급망 위험입니다.

## 13. 언어

주석과 문서는 한국어로 씁니다. Go doc 주석, README, Makefile 도움말, 스크립트 메시지, CLI
출력과 에러 메시지, 커밋 메시지 본문이 해당합니다.

다음은 영어로 유지합니다. 바꾸면 깨지거나 관례를 벗어납니다.

- 식별자 전부 — 패키지, 타입, 함수, 변수, 상수
- 출력 계약 — 리소스 타입 ID(`ec2:instance`), 관계 이름, JSON 키, CSV 헤더
- `Test` 접두사 테스트 함수명, Conventional Commit 접두사(`feat:`, `fix:`)
- AWS API 이름과 에러 코드(`AccessDeniedException`), CLI 플래그 이름

Go doc 주석은 한국어에서도 식별자 이름으로 시작하고 마침표로 끝냅니다. `revive`의 `exported`와
`godot`이 검사합니다.

```go
// SortResources는 리소스를 제자리에서 결정적으로 정렬한다.
func SortResources(...)
```

테스트 실패 메시지는 `got = %q, want %q` 관용구를 유지하고, 그 위의 설명 주석은 한국어로 씁니다.

## 참고

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- [Go Proverbs](https://go-proverbs.github.io/)
