package model

import (
	"fmt"
	"time"
)

// SchemaVersion은 스냅샷과 리포트 형식의 버전이다.
//
// JSON 구조가 호환되지 않게 바뀌면 올린다. 저장된 스냅샷을 읽는 쪽이 이 값을 보고
// 파싱 가능 여부를 판단한다.
const SchemaVersion = 1

// Snapshot은 하나의 완결된 수집 실행이다. 무엇을 요청했고, 무엇이 돌아왔고, 무엇이
// 실패했는지를 담는다. 리포트가 렌더링하고 캐시가 저장하는 단위다.
type Snapshot struct {
	SchemaVersion int            `json:"schemaVersion"`
	GeneratedAt   time.Time      `json:"generatedAt"`
	Tool          ToolInfo       `json:"tool"`
	Scope         Scope          `json:"scope"`
	Summary       Summary        `json:"summary"`
	Resources     []Resource     `json:"resources"`
	Errors        []CollectError `json:"errors"`
}

// ToolInfo는 이 스냅샷을 만든 빌드를 기록한다. 오래된 파일을 나중에 열었을 때 동작이
// 달랐던 버전의 산출물임을 알아볼 수 있게 한다.
type ToolInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Scope는 이 실행이 무엇을 보도록 지시받았는지 기록한다.
type Scope struct {
	Profile   string   `json:"profile"`
	AccountID string   `json:"accountId"`
	ARN       string   `json:"arn,omitempty"`
	Regions   []string `json:"regions"`
	Types     []string `json:"types"`
}

// Summary는 리소스와 에러에서 파생된 집계값을 담는다.
//
// ByType이 map인 이유는 JSON 객체의 키 순서에는 의미가 없고, encoding/json이 map 키를
// 정렬해서 내보내므로 출력이 그대로 재현 가능하기 때문이다.
type Summary struct {
	ResourceCount int            `json:"resourceCount"`
	ByType        map[string]int `json:"byType"`
	ErrorCount    int            `json:"errorCount"`
}

// CollectError는 읽지 못한 범위를 기록한다.
//
// 이 값은 데이터를 대체하지 않고 데이터와 함께 실린다. 한 리전의 권한 부족이 그 실행이
// 수집해낸 나머지 전부를 버리게 만들면 안 되므로, 호출자는 양쪽을 함께 보고한다.
//
// Code는 추출할 수 있었을 때의 공급자 에러 코드다. Message는 진단용 원본 에러 문구다.
// Explanation은 사용자에게 보여줄 문장이며 awsclient.Explain이 만들어낸다.
type CollectError struct {
	Type        string `json:"type"`
	Profile     string `json:"profile"`
	Region      string `json:"region"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message"`
	Explanation string `json:"explanation,omitempty"`
}

// Error는 error 인터페이스를 구현한다. 덕분에 수집 실패를 표준 errors 헬퍼로 묶고
// 조사할 수 있다.
func (e CollectError) Error() string {
	return fmt.Sprintf("%s (%s/%s): %s", e.Type, e.Profile, e.Region, e.Message)
}

// NewSnapshot은 스냅샷을 조립하면서 리소스를 정렬하고 요약을 계산한다.
//
// now를 time.Now 호출이 아니라 인자로 받는 이유는 테스트를 포함한 호출자가 타임스탬프를
// 통제할 수 있게 하기 위함이다.
//
// nil 슬라이스는 빈 슬라이스로 바꾼다. nil 슬라이스는 JSON에서 null로, 빈 슬라이스는
// []로 직렬화되는데, 문서화된 스키마에는 배열이 항상 존재한다. 여기서 정규화해두면
// 소비하는 쪽이 null을 특수 처리할 필요가 없다.
func NewSnapshot(now time.Time, tool ToolInfo, scope Scope, resources []Resource, errs []CollectError) Snapshot {
	sorted := make([]Resource, len(resources))
	copy(sorted, resources)
	SortResources(sorted)

	byType := make(map[string]int, len(sorted))

	for i := range sorted {
		byType[sorted[i].Type]++

		if sorted[i].Fields == nil {
			sorted[i].Fields = []Field{}
		}

		if sorted[i].Tags == nil {
			sorted[i].Tags = []Field{}
		}

		if sorted[i].Related == nil {
			sorted[i].Related = []Ref{}
		}
	}

	if errs == nil {
		errs = []CollectError{}
	}

	if scope.Regions == nil {
		scope.Regions = []string{}
	}

	if scope.Types == nil {
		scope.Types = []string{}
	}

	return Snapshot{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now.UTC(),
		Tool:          tool,
		Scope:         scope,
		Summary: Summary{
			ResourceCount: len(sorted),
			ByType:        byType,
			ErrorCount:    len(errs),
		},
		Resources: sorted,
		Errors:    errs,
	}
}
