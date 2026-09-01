package elbv2

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeLoadBalancersAPI는 로드밸런서 수집기가 필요로 하는 SDK 메서드만 담은
// 인터페이스다. 메서드 이름이 Describe로 시작하므로 조회 전용 가드를 통과한다.
type describeLoadBalancersAPI interface {
	DescribeLoadBalancers(context.Context, *awselbv2.DescribeLoadBalancersInput, ...func(*awselbv2.Options)) (*awselbv2.DescribeLoadBalancersOutput, error)
}

// loadBalancerCollector는 ALB/NLB(Elastic Load Balancing v2)를 조회한다.
type loadBalancerCollector struct {
	api describeLoadBalancersAPI
}

// NewLoadBalancer는 로드밸런서 수집기를 만든다.
func NewLoadBalancer(api describeLoadBalancersAPI) collect.Collector {
	return loadBalancerCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c loadBalancerCollector) Type() string { return model.TypeELBv2LoadBalancer }

// Collect는 범위 안의 ALB/NLB를 모두 조회해 도메인 리소스로 변환한다.
func (c loadBalancerCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awselbv2.NewDescribeLoadBalancersPaginator(c.api, &awselbv2.DescribeLoadBalancersInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe load balancers: %w", err)
		}

		for i := range page.LoadBalancers {
			out = append(out, loadBalancerToResource(req.Scope, page.LoadBalancers[i]))
		}
	}

	return out, nil
}

// loadBalancerToResource는 SDK 로드밸런서를 도메인 리소스로 변환한다.
//
// ID로는 이름(LoadBalancerName)을 쓴다. ARN이 더 유일하지만 길어서 목록에서 읽기
// 어렵고, 이름이 한 리전·계정 안에서 유일하다. ARN은 ARN 필드에 따로 담아 상세·리포트에서
// 참조할 수 있게 한다.
func loadBalancerToResource(scope collect.Scope, lb elbv2types.LoadBalancer) model.Resource {
	dnsName := aws.ToString(lb.DNSName)
	r := model.Resource{
		Type:      model.TypeELBv2LoadBalancer,
		ID:        aws.ToString(lb.LoadBalancerName),
		Name:      aws.ToString(lb.LoadBalancerName),
		ARN:       aws.ToString(lb.LoadBalancerArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
	}

	if dnsName != "" {
		r.Identifiers = loadBalancerDNSIdentifiers(dnsName)
	}

	if lb.State != nil {
		r.Status = string(lb.State.Code)
	}

	if lb.CreatedTime != nil {
		t := lb.CreatedTime.UTC()
		r.CreatedAt = &t
	}

	r.Fields = []model.Field{
		{Key: "종류", Value: string(lb.Type)},
		{Key: "스킴", Value: string(lb.Scheme)},
		{Key: "DNS 이름", Value: displayString(dnsName)},
		{Key: "VPC", Value: displayString(aws.ToString(lb.VpcId))},
		{Key: "가용 영역", Value: displayInt32(int32(len(lb.AvailabilityZones)))},
	}

	// 관계(로드밸런서 → 타깃 그룹)는 리스너를 따라가야 알 수 있다. 타깃 그룹 수집기가
	// 반대 방향(target group → load balancer)을 남기므로, 3단계 그래프에서 연결된다.

	return r
}

// loadBalancerDNSIdentifiers는 실제 DNS 이름과 Route 53 alias에서 사용하는 dualstack
// 이름을 모두 제공한다. graph의 일반 DNS 정규화에 ELB 전용 규칙을 섞지 않기 위함이다.
func loadBalancerDNSIdentifiers(dnsName string) []model.Identifier {
	identifiers := []model.Identifier{{Kind: model.IdentifierDNS, Value: dnsName}}
	if !strings.HasPrefix(strings.ToLower(dnsName), "dualstack.") {
		identifiers = append(identifiers, model.Identifier{
			Kind: model.IdentifierDNS, Value: "dualstack." + dnsName,
		})
	}

	return identifiers
}

// displayString은 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func displayString(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// displayInt32는 int32를 표시용 문자열로 바꾼다.
func displayInt32(n int32) string {
	return strconv.Itoa(int(n))
}
