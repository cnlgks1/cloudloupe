# cloudloupe Go 설계 원칙

이 문서는 cloudloupe 구현 시 항상 지켜야 하는 설계 규약이다.
새 코드를 쓰거나 리뷰할 때 이 원칙을 기준으로 판단한다.

---

## 1. 조회 전용은 타입과 CI로 강제한다 (최우선)

"쓰기 API를 안 쓴다"는 약속이 아니라 구조로 막는다.

- 수집기 인터페이스에 쓰기 경로가 존재하지 않는다. 메서드는 `Collect` 하나뿐이다.
- AWS 호출은 **allow-list** 방식으로만 허용한다. deny-list(금지 목록)는 새 API가 추가되면 뚫린다.
  - 허용 접두사: `Describe`, `List`, `Get`, `Lookup`, `Search`, `BatchGet`
  - 그 외 모든 SDK 메서드 호출은 CI에서 실패시킨다 (`scripts/verify-readonly.sh`).
- 자격증명은 읽기만 한다. `~/.aws/*` 파일에 쓰지 않는다.
- 로컬 쓰기가 허용되는 유일한 대상: SQLite 캐시 파일, 사용자가 지정한 리포트 출력 경로.

```go
// 이 인터페이스가 조회 전용의 경계다. 여기에 Apply/Delete/Modify가 생기면 설계 위반.
type Collector interface {
    Type() string
    Collect(ctx context.Context, req Request) ([]model.Resource, error)
}
```

## 2. 의존성 방향은 한 방향으로만 흐른다

```
cmd/cloudloupe
      ↓
internal/tui ──────────────┐
      ↓                    │
internal/collect           │  (모두 model만 의존)
      ↓                    ↓
internal/awsclient    internal/{graph,findings,report,cache}
      ↓                    ↓
   AWS SDK          internal/model  ← 외부 의존성 0
```

- **`internal/model`을 신설한다.** `Resource`, `Ref`, `Field` 같은 핵심 도메인 타입이 여기 산다.
  이유: `graph`/`findings`/`report`/`cache`가 타입 하나 때문에 AWS SDK 전체를 전이 의존하면 안 된다.
  `model`은 표준 라이브러리 외에 아무것도 import하지 않는다.
- 도메인 패키지(`collect`, `graph`, `findings`, `report`, `cache`)는 **`tui`를 절대 import하지 않는다.**
  TUI는 표현 계층일 뿐이며, 헤드리스 모드에서 도메인이 단독으로 동작해야 한다.
- 순환 의존이 생기려 하면 공통 타입을 더 낮은 계층으로 내린다. 인터페이스로 우회하지 않는다.

## 3. 인터페이스는 쓰는 쪽에서, 작게 정의한다

- Go 관용: **구조체를 반환하고, 인터페이스를 받는다.**
- SDK 클라이언트 전체를 목킹하지 말고, 필요한 호출만 좁은 인터페이스로 자른다.
  자격증명 없이 단위 테스트가 가능해지는 지점이다.

```go
// 수집기가 필요한 건 이 메서드 하나뿐이다.
type instancesAPI interface {
    DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}
```

- 인터페이스 메서드는 1~3개를 목표로 한다. 커지면 책임이 섞인 신호다.
- 구현이 하나뿐인데 "나중에 대비"로 인터페이스를 만들지 않는다. 두 번째 구현이나 테스트 대역이 필요할 때 만든다.

## 4. context는 첫 번째 인자, 구조체에 저장하지 않는다

- I/O를 하는 모든 함수는 `ctx context.Context`를 첫 인자로 받는다.
- `context`를 구조체 필드에 보관하지 않는다.
- 타임아웃 정책은 최상위 호출자(`cmd`)가 정한다. 라이브러리 계층이 자기 마음대로 데드라인을 걸지 않는다.
- TUI의 수집 작업은 반드시 취소 가능해야 한다. `esc`로 진행 중인 수집을 끊을 수 있어야 한다.

## 5. 부분 실패는 전체 실패가 아니다

멀티 계정·멀티 리전 조회에서 가장 중요한 에러 설계다.

- 리전 하나에서 권한이 부족하다고 전체 조회가 죽으면 도구가 쓸모없어진다.
- 수집 결과와 에러를 **함께** 반환한다. 성공한 리전 데이터는 살린다.

```go
type Result struct {
    Resources []model.Resource
    Errors    []CollectError // (type, profile, region, err)
}
```

- 에러는 문맥을 붙여 감싼다: `fmt.Errorf("describe instances (%s/%s): %w", profile, region, err)`
- 판별은 `errors.Is` / `errors.As`. AWS 에러 코드는 `smithy.APIError`로 꺼낸다. 문자열 매칭은 최후수단.
- **사용자 대면 메시지와 진단 메시지를 분리한다.** `awsclient.Explain(err)`이 자격증명 없음 / 권한 부족 /
  토큰 만료 / 리전 미지원을 사람이 읽을 수 있는 문장으로 바꾼다. 원본 에러는 디버그 뷰에 남긴다.
- `panic`은 프로그래밍 오류에만. TUI에서 panic이 터지면 터미널이 깨지므로 최상위에서 recover 후 터미널을 복구한다.

## 6. 고루틴은 상한을 두고, 수명을 호출자가 소유한다

- 팬아웃은 `errgroup.WithContext` + `SetLimit` 또는 세마포어로 **동시 실행 수를 제한한다.**
  리전 × 리소스 타입은 곱셈으로 늘어난다. 무제한 고루틴은 API 스로틀링을 부른다.
- 함수가 만든 고루틴은 그 함수가 리턴하기 전에 모두 정리된다. 백그라운드로 새는 고루틴을 만들지 않는다.
- 채널 파이프라인보다 뮤텍스로 보호한 슬라이스가 단순하면 그쪽을 택한다. 영리함보다 명확함.
- 수집 결과는 완성된 뒤 **불변 스냅샷**으로 넘긴다. 공유 가변 상태를 여러 계층이 들여다보지 않는다.

## 7. Bubble Tea는 Elm 규율을 지킨다

- `Model`은 상태, `Update`는 상태 전이, `View`는 순수 렌더링.
- **`View`에서 I/O나 상태 변경 금지.** 문자열만 만든다.
- 부수효과는 전부 `tea.Cmd`로 내보내고 결과는 `tea.Msg`로 받는다.
- **모델 필드를 다른 고루틴에서 절대 만지지 않는다.** Bubble Tea 런타임이 단일 고루틴 갱신을 보장하는 걸 깨면
  진단 불가능한 레이스가 된다. 백그라운드 수집 → `tea.Cmd` → `collectDoneMsg` 경로만 사용.
- 화면 전환은 명시적 상태 enum + `switch`. 화면별 로직이 커지면 자체 `Update`/`View`를 가진 서브모델로 분리한다.
- 키 바인딩은 `key.Binding`으로 한 곳에 모아 help 뷰와 공유한다. 키 문자열을 `Update` 안에 흩뿌리지 않는다.
- 스타일은 **테마 주입**으로 처리한다. `if ascii` 분기를 렌더링 코드마다 넣지 말고,
  테마가 박스 문자/글리프를 들고 있게 해서 호출부는 테마만 쓴다.

## 8. 데이터 모델은 결정적으로 렌더링된다

- **표시 순서가 의미 있는 데이터에 `map`을 쓰지 않는다.** Go의 map 순회는 순서가 무작위라
  같은 리소스를 다시 열 때마다 상세 뷰 필드 순서가 바뀐다. `[]Field{Key, Value}`를 쓴다.
- 시간은 `time.Time`(UTC)으로 보관하고 표시 직전에 포맷한다.
- AWS SDK의 포인터는 **경계에서 값으로 변환한다** (`aws.ToString`, `aws.ToInt32`).
  포인터가 도메인 모델 안까지 들어오면 nil 체크가 전염된다.
- zero value가 의미 있게 동작하도록 설계한다.

## 9. 등록은 명시적으로, 전역 상태는 없다

- 수집기 레지스트리를 `init()` 부수효과로 채우지 않는다. 한 곳에서 명시적으로 조립한다.

```go
func DefaultRegistry() *Registry {
    r := NewRegistry()
    r.Add(ec2InstanceCollector{})
    r.Add(elbv2LoadBalancerCollector{})
    return r
}
```

  이유: import 부수효과에 의존한 등록은 추적이 어렵고 테스트에서 격리가 안 된다.
- 패키지 수준 가변 전역 변수를 두지 않는다. 예외는 ldflags로 주입되는 `version`/`commit`/`date`.
- 의존성은 생성자 인자로 명시적으로 주입한다.

## 10. 명명은 Go 관용을 따른다

- 패키지명은 짧은 소문자 단수. `util`, `common`, `helpers`, `base`는 금지 — 책임을 설명하지 않는다.
- 스터터링 회피: `collect.Collector` (o) / `collect.CollectCollector` (x)
- 필요한 것만 export한다. 모든 코드는 `internal/` 아래 두어 외부 import를 원천 차단한다.
- 생성자는 `New`, `NewXxx`. 인자가 많아지면 functional options.
- getter에 `Get` 접두사를 붙이지 않는다 (`r.Name()`, not `r.GetName()`).

## 11. 테스트는 자격증명 없이 돌아간다

- AWS 호출은 좁은 인터페이스 경계에서 fake로 대체한다. **테스트가 실제 AWS를 때리지 않는다.**
- 순수 로직(`graph`, `findings`, `report`)은 테이블 드리븐 테스트로 덮는다.
- 리포트 출력은 golden 파일로 검증한다.
- TUI는 `Update(msg)` 를 직접 호출해 "메시지 입력 → 기대 상태" 로 테스트한다. 렌더링 문자열 비교는 깨지기 쉽다.

## 12. 크로스 플랫폼과 의존성 최소주의

- `CGO_ENABLED=0`을 유지한다. 순수 Go 의존만 쓴다 (SQLite는 `modernc.org/sqlite`).
  단일 정적 바이너리가 배포 전략의 전제다.
- 경로는 항상 `filepath`, 홈 디렉터리는 `os.UserHomeDir()`. 구분자나 경로를 하드코딩하지 않는다.
- OS 분기는 `runtime.GOOS` 산재보다 파일명 suffix(`_windows.go` / `_unix.go`)를 우선한다.
- 새 의존성을 추가하기 전에 표준 라이브러리나 이미 있는 의존성으로 되는지 먼저 확인한다.
  터미널 UI 도구의 의존성 트리는 그대로 공급망 위험이다.

## 13. 언어: 주석과 문서는 한국어로 쓴다

이 프로젝트의 1차 사용자와 관리자는 한국어 사용자다. 영어로 쓰는 비용을 들이지 않는다.

**한국어로 쓴다:**

- Go doc 주석과 인라인 주석 전부
- README
- 워크플로 주석, Makefile 도움말, 스크립트 메시지
- 커밋 메시지 본문, 사용자에게 보이는 CLI 출력과 에러 메시지

**영어로 유지한다** (바꾸면 깨지거나 관례를 벗어나는 것들):

- 모든 식별자 — 패키지·타입·함수·변수·상수 이름
- 리소스 타입 ID(`ec2:instance`), 관계 이름(`attached-eni`), JSON 키(`accountId`), CSV 헤더
  → 이건 출력 계약이다. 번역하면 소비자가 깨진다.
- `Test`로 시작하는 테스트 함수 이름 (Go가 요구)
- Conventional Commit 접두사 (`feat:`, `fix:`, `ci:`)
- AWS API 이름, 에러 코드(`AccessDeniedException`), CLI 플래그 이름
- README 상단 태그라인 (프로젝트 정체성으로 확정된 문구)

**Go doc 주석 작성 규칙:**

식별자 이름으로 시작하는 Go 관례는 한국어에서도 지킨다. `revive`의 `exported` 규칙이 이를 검사한다.

```go
// Resource는 수집된 AWS 리소스 하나를 나타낸다.
func ...

// SortResources는 리소스를 제자리에서 결정적으로 정렬한다.
func SortResources(...)
```

문장은 마침표로 끝낸다. `godot` 린터가 이를 검사한다.

테스트 실패 메시지는 `got = %q, want %q` 관용구를 유지한다. 짧고 Go 개발자에게 즉시 읽히며,
번역해서 얻는 게 없다. 다만 그 위의 설명 주석 — 왜 이 테스트가 존재하는지 — 은 한국어로 쓴다.
