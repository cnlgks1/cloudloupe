package ecs

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeServicesBatch는 DescribeServices가 한 번에 받는 서비스 개수 상한이다.
//
// ECS API는 요청당 최대 10개까지만 상세를 준다. ListServices가 준 ARN을 이 크기로 잘라
// 여러 번 부른다.
const describeServicesBatch = 10

// serviceAPI는 서비스 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 서비스는 클러스터 하위에 있어 먼저 ListClusters로 클러스터를 찾고, 클러스터마다
// ListServices로 서비스 ARN을, DescribeServices로 상세를 받는다.
type serviceAPI interface {
	ListClusters(context.Context, *awsecs.ListClustersInput, ...func(*awsecs.Options)) (*awsecs.ListClustersOutput, error)
	ListServices(context.Context, *awsecs.ListServicesInput, ...func(*awsecs.Options)) (*awsecs.ListServicesOutput, error)
	DescribeServices(context.Context, *awsecs.DescribeServicesInput, ...func(*awsecs.Options)) (*awsecs.DescribeServicesOutput, error)
}

// serviceCollector는 ECS 서비스를 조회한다.
type serviceCollector struct {
	api serviceAPI
	// limit은 클러스터별 조회 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewService는 ECS 서비스 수집기를 만든다.
func NewService(api serviceAPI) collect.Collector {
	return serviceCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c serviceCollector) Type() string { return model.TypeECSService }

// Collect는 리전의 ECS 서비스를 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListClusters로 클러스터 ARN 목록을 받는다(페이지네이션).
//  2. 클러스터마다 ListServices로 서비스 ARN을 받고 DescribeServices로 상세를 받는다.
//     클러스터 단위로 상한 있는 팬아웃을 돌린다.
//
// 클러스터 목록 조회가 중간에 실패하면 그때까지 받은 클러스터로 계속 진행한다. 특정
// 클러스터 조회가 실패해도 나머지 클러스터의 서비스는 살린다. 부분 실패는 모두 수집한
// 리소스와 함께 반환된다.
func (c serviceCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	clusterARNs, listErr := c.clusterARNs(ctx)
	if len(clusterARNs) == 0 {
		return nil, listErr
	}

	perCluster, fanErr := collect.FanOut(ctx, c.limit, clusterARNs,
		c.servicesForCluster)

	out := make([]model.Resource, 0)
	for _, services := range perCluster {
		for i := range services {
			out = append(out, serviceToResource(req.Scope, services[i]))
		}
	}

	return out, errors.Join(listErr, fanErr)
}

// clusterARNs는 리전의 클러스터 ARN 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c serviceCollector) clusterARNs(ctx context.Context) ([]string, error) {
	paginator := awsecs.NewListClustersPaginator(c.api, &awsecs.ListClustersInput{})

	var arns []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return arns, fmt.Errorf("list clusters: %w", err)
		}

		arns = append(arns, page.ClusterArns...)
	}

	return arns, nil
}

// servicesForCluster는 클러스터 하나의 서비스 상세를 모두 받는다.
//
// ListServices로 서비스 ARN을 모으고 최대 describeServicesBatch개씩 잘라 DescribeServices를
// 부른다. 배치 하나가 실패해도 나머지 배치와 이미 받은 결과는 살린다.
func (c serviceCollector) servicesForCluster(ctx context.Context, clusterARN string) ([]ecstypes.Service, error) {
	paginator := awsecs.NewListServicesPaginator(c.api, &awsecs.ListServicesInput{
		Cluster: aws.String(clusterARN),
	})

	var arns []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list services (%s): %w", clusterARN, err)
		}

		arns = append(arns, page.ServiceArns...)
	}

	var (
		services []ecstypes.Service
		errs     []error
	)

	for start := 0; start < len(arns); start += describeServicesBatch {
		end := start + describeServicesBatch
		if end > len(arns) {
			end = len(arns)
		}

		out, err := c.api.DescribeServices(ctx, &awsecs.DescribeServicesInput{
			Cluster:  aws.String(clusterARN),
			Services: arns[start:end],
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("describe services (%s): %w", clusterARN, err))

			continue
		}

		services = append(services, out.Services...)
	}

	return services, errors.Join(errs...)
}

// serviceToResource는 SDK 서비스를 도메인 리소스로 변환한다.
//
// ID·이름은 ServiceName, ARN은 ServiceArn, 상태는 Status를 그대로 쓴다. 관계 이름에는
// 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func serviceToResource(scope collect.Scope, service ecstypes.Service) model.Resource {
	var refs []model.Ref

	refs = appendARNRef(refs, model.TypeECSCluster, "ClusterArn", aws.ToString(service.ClusterArn))
	refs = appendARNRef(refs, model.TypeECSTaskDefinition, "TaskDefinition", aws.ToString(service.TaskDefinition))

	if service.NetworkConfiguration != nil && service.NetworkConfiguration.AwsvpcConfiguration != nil {
		vpc := service.NetworkConfiguration.AwsvpcConfiguration
		for _, subnet := range vpc.Subnets {
			refs = appendIDRef(refs, model.TypeEC2Subnet, "NetworkConfiguration.AwsvpcConfiguration.Subnets", subnet)
		}

		for _, sg := range vpc.SecurityGroups {
			refs = appendIDRef(refs, model.TypeEC2SecurityGroup, "NetworkConfiguration.AwsvpcConfiguration.SecurityGroups", sg)
		}
	}

	for _, lb := range service.LoadBalancers {
		refs = appendARNRef(refs, model.TypeELBv2TargetGroup, "LoadBalancers.TargetGroupArn", aws.ToString(lb.TargetGroupArn))
	}

	return model.Resource{
		Type:      model.TypeECSService,
		ID:        aws.ToString(service.ServiceName),
		Name:      aws.ToString(service.ServiceName),
		ARN:       aws.ToString(service.ServiceArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(service.Status),
		Fields: []model.Field{
			{Key: "Status", Value: orDash(aws.ToString(service.Status))},
			{Key: "LaunchType", Value: orDash(string(service.LaunchType))},
			{Key: "DesiredCount", Value: strconv.Itoa(int(service.DesiredCount))},
			{Key: "RunningCount", Value: strconv.Itoa(int(service.RunningCount))},
			{Key: "PendingCount", Value: strconv.Itoa(int(service.PendingCount))},
			{Key: "TaskDefinition", Value: orDash(aws.ToString(service.TaskDefinition))},
			{Key: "PlatformVersion", Value: orDash(aws.ToString(service.PlatformVersion))},
			{Key: "SchedulingStrategy", Value: orDash(string(service.SchedulingStrategy))},
		},
		Related: refs,
	}
}
