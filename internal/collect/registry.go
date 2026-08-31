package collect

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// Clients는 수집기들이 사용할 AWS 서비스 클라이언트 묶음이다.
//
// 한 범위(프로필+리전)에 대해 만들어진 클라이언트들을 담는다. 인터페이스가 아니라 구체
// 타입을 담는 이유는, 좁은 인터페이스는 각 수집기가 자기 파일에서 정의하기 때문이다
// (describeInstancesAPI 등). 여기서는 그 클라이언트를 넘겨주기만 한다.
//
// 지금은 EC2뿐이다. elbv2, route53, wafv2 클라이언트는 해당 수집기가 추가될 때 함께
// 늘어난다.
type Clients struct {
	EC2 *ec2.Client
}

// DefaultRegistry는 기본 수집기들을 명시적으로 조립한다.
//
// init() 부수효과로 등록하지 않는 이유가 여기 드러난다. 어떤 수집기가 등록되는지 이
// 함수 하나만 읽으면 전부 알 수 있고, 테스트에서 원하는 수집기만 담은 레지스트리를
// 따로 만들 수 있다. 등록 순서는 조회·표시 순서에 영향을 주므로 의미가 있다.
//
// 새 리소스 타입을 추가하는 절차는 세 줄이다.
//  1. <서비스>_<리소스>.go 에 좁은 API 인터페이스와 Collector 구현을 만든다.
//  2. Clients에 필요한 클라이언트를 추가한다(이미 있으면 생략).
//  3. 여기에 r.Add(...) 한 줄을 넣는다.
func DefaultRegistry(clients Clients) *Registry {
	r := NewRegistry()

	r.Add(NewEC2InstanceCollector(clients.EC2))

	return r
}
