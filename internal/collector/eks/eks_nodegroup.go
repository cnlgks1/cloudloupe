package eks

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// nodegroupAPI는 노드그룹 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 노드그룹은 클러스터 하위에 있어 먼저 ListClusters로 클러스터를 찾고, 클러스터마다
// ListNodegroups로 이름을, DescribeNodegroup으로 상세를 받는다.
type nodegroupAPI interface {
	ListClusters(context.Context, *awseks.ListClustersInput, ...func(*awseks.Options)) (*awseks.ListClustersOutput, error)
	ListNodegroups(context.Context, *awseks.ListNodegroupsInput, ...func(*awseks.Options)) (*awseks.ListNodegroupsOutput, error)
	DescribeNodegroup(context.Context, *awseks.DescribeNodegroupInput, ...func(*awseks.Options)) (*awseks.DescribeNodegroupOutput, error)
}

// nodegroupCollector는 EKS 노드그룹을 조회한다.
type nodegroupCollector struct {
	api nodegroupAPI
	// limit은 클러스터별 조회 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewNodegroup은 EKS 노드그룹 수집기를 만든다.
func NewNodegroup(api nodegroupAPI) collect.Collector {
	return nodegroupCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c nodegroupCollector) Type() string { return model.TypeEKSNodegroup }

// Collect는 리전의 EKS 노드그룹을 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListClusters로 클러스터 이름 목록을 받는다(페이지네이션).
//  2. 클러스터마다 ListNodegroups로 노드그룹 이름을 받고 DescribeNodegroup으로 상세를
//     받는다. 클러스터 단위로 상한 있는 팬아웃을 돌린다.
//
// 클러스터 목록 조회가 중간에 실패하면 그때까지 받은 클러스터로 계속 진행한다. 특정
// 클러스터 조회가 실패해도 나머지 클러스터의 노드그룹은 살린다. 부분 실패는 모두 수집한
// 리소스와 함께 반환된다.
func (c nodegroupCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	clusterNames, listErr := c.clusterNames(ctx)
	if len(clusterNames) == 0 {
		return nil, listErr
	}

	perCluster, fanErr := collect.FanOut(ctx, c.limit, clusterNames, c.nodegroupsForCluster)

	out := make([]model.Resource, 0)
	for _, nodegroups := range perCluster {
		for i := range nodegroups {
			out = append(out, nodegroupToResource(req.Scope, nodegroups[i]))
		}
	}

	return out, errors.Join(listErr, fanErr)
}

// clusterNames는 리전의 클러스터 이름 목록을 모두 받는다.
func (c nodegroupCollector) clusterNames(ctx context.Context) ([]string, error) {
	paginator := awseks.NewListClustersPaginator(c.api, &awseks.ListClustersInput{})

	var names []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return names, fmt.Errorf("list clusters: %w", err)
		}

		names = append(names, page.Clusters...)
	}

	return names, nil
}

// nodegroupsForCluster는 클러스터 하나의 노드그룹 상세를 모두 받는다.
//
// ListNodegroups로 이름을 모으고 이름마다 DescribeNodegroup을 부른다. 상세 조회 하나가
// 실패해도 나머지와 이미 받은 결과는 살린다.
func (c nodegroupCollector) nodegroupsForCluster(ctx context.Context, clusterName string) ([]ekstypes.Nodegroup, error) {
	paginator := awseks.NewListNodegroupsPaginator(c.api, &awseks.ListNodegroupsInput{
		ClusterName: aws.String(clusterName),
	})

	var names []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list nodegroups (%s): %w", clusterName, err)
		}

		names = append(names, page.Nodegroups...)
	}

	var (
		nodegroups []ekstypes.Nodegroup
		errs       []error
	)

	for _, name := range names {
		out, err := c.api.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
			ClusterName:   aws.String(clusterName),
			NodegroupName: aws.String(name),
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("describe nodegroup (%s/%s): %w", clusterName, name, err))

			continue
		}
		if out.Nodegroup == nil {
			continue
		}

		nodegroups = append(nodegroups, *out.Nodegroup)
	}

	return nodegroups, errors.Join(errs...)
}

// nodegroupToResource는 SDK 노드그룹을 도메인 리소스로 변환한다.
//
// ID·이름은 NodegroupName, ARN은 NodegroupArn을 그대로 쓴다. 관계 이름에는 값을 꺼낸 SDK
// 응답 필드 경로를 넣는다.
func nodegroupToResource(scope collect.Scope, ng ekstypes.Nodegroup) model.Resource {
	var refs []model.Ref

	refs = appendIDRef(refs, model.TypeEKSCluster, "ClusterName", aws.ToString(ng.ClusterName))
	refs = appendARNRef(refs, model.TypeIAMRole, "NodeRole", aws.ToString(ng.NodeRole))

	for _, subnet := range ng.Subnets {
		refs = appendIDRef(refs, model.TypeEC2Subnet, "Subnets", subnet)
	}

	if ng.Resources != nil {
		for _, asg := range ng.Resources.AutoScalingGroups {
			refs = appendIDRef(refs, model.TypeAutoScalingGroup, "Resources.AutoScalingGroups.Name", aws.ToString(asg.Name))
		}
	}

	return model.Resource{
		Type:      model.TypeEKSNodegroup,
		ID:        aws.ToString(ng.NodegroupName),
		Name:      aws.ToString(ng.NodegroupName),
		ARN:       aws.ToString(ng.NodegroupArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(ng.Status),
		CreatedAt: ng.CreatedAt,
		Fields: []model.Field{
			{Key: "ClusterName", Value: orDash(aws.ToString(ng.ClusterName))},
			{Key: "Status", Value: orDash(string(ng.Status))},
			{Key: "InstanceTypes", Value: orDash(joinValues(ng.InstanceTypes))},
			{Key: "AmiType", Value: orDash(string(ng.AmiType))},
			{Key: "CapacityType", Value: orDash(string(ng.CapacityType))},
			{Key: "DesiredSize", Value: scalingSize(ng.ScalingConfig, scalingDesired)},
			{Key: "MinSize", Value: scalingSize(ng.ScalingConfig, scalingMin)},
			{Key: "MaxSize", Value: scalingSize(ng.ScalingConfig, scalingMax)},
			{Key: "DiskSize", Value: int32PtrValue(ng.DiskSize)},
			{Key: "Version", Value: orDash(aws.ToString(ng.Version))},
			{Key: "NodeRole", Value: orDash(aws.ToString(ng.NodeRole))},
		},
		Related: refs,
	}
}

// scalingField는 스케일링 설정에서 꺼낼 값의 종류다.
type scalingField uint8

const (
	scalingDesired scalingField = iota
	scalingMin
	scalingMax
)

// scalingSize는 스케일링 설정의 선택한 값을 API 정수 그대로 표시한다. 설정이 없으면 "-"다.
func scalingSize(cfg *ekstypes.NodegroupScalingConfig, field scalingField) string {
	if cfg == nil {
		return "-"
	}

	switch field {
	case scalingDesired:
		return int32PtrValue(cfg.DesiredSize)
	case scalingMin:
		return int32PtrValue(cfg.MinSize)
	case scalingMax:
		return int32PtrValue(cfg.MaxSize)
	default:
		return "-"
	}
}

// int32PtrValue는 선택적인 정수 값을 API가 준 단위 그대로 표시한다. nil이면 "-"다.
func int32PtrValue(value *int32) string {
	if value == nil {
		return "-"
	}

	return strconv.Itoa(int(*value))
}
