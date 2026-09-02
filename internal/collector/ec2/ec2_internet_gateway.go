package ec2

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeInternetGatewaysAPI는 인터넷 게이트웨이 수집기가 필요한 SDK 메서드만 담는다.
type describeInternetGatewaysAPI interface {
	DescribeInternetGateways(context.Context, *awsec2.DescribeInternetGatewaysInput, ...func(*awsec2.Options)) (*awsec2.DescribeInternetGatewaysOutput, error)
}

// internetGatewayCollector는 인터넷 게이트웨이를 조회한다.
type internetGatewayCollector struct {
	api describeInternetGatewaysAPI
}

// NewInternetGateway는 인터넷 게이트웨이 수집기를 만든다.
func NewInternetGateway(api describeInternetGatewaysAPI) collect.Collector {
	return internetGatewayCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c internetGatewayCollector) Type() string { return model.TypeEC2InternetGateway }

// Collect는 범위 안의 인터넷 게이트웨이를 모두 조회해 도메인 리소스로 변환한다.
func (c internetGatewayCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeInternetGatewaysPaginator(c.api, &awsec2.DescribeInternetGatewaysInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe internet gateways: %w", err)
		}

		for i := range page.InternetGateways {
			out = append(out, internetGatewayToResource(req.Scope, page.InternetGateways[i]))
		}
	}

	return out, nil
}

// internetGatewayToResource는 SDK 인터넷 게이트웨이를 도메인 리소스로 변환한다.
func internetGatewayToResource(scope collect.Scope, gateway ec2types.InternetGateway) model.Resource {
	vpcIDs := make([]string, 0, len(gateway.Attachments))
	states := make([]string, 0, len(gateway.Attachments))
	refs := make([]model.Ref, 0, len(gateway.Attachments))

	for _, attachment := range gateway.Attachments {
		if id := aws.ToString(attachment.VpcId); id != "" {
			vpcIDs = append(vpcIDs, id)
			refs = append(refs, model.Ref{Type: model.TypeEC2VPC, ID: id, Relation: model.RelationAttachedTo})
		}
		if state := string(attachment.State); state != "" {
			states = append(states, state)
		}
	}

	// AWS는 VPC에 붙어 있을 때만 attachment 상태(available)를 준다. 떨어진 게이트웨이는
	// 상태가 비므로 그때만 우리가 값을 만든다.
	status := "detached"
	if len(states) > 0 {
		status = strings.Join(states, ", ")
	}

	return model.Resource{
		Type:      model.TypeEC2InternetGateway,
		ID:        aws.ToString(gateway.InternetGatewayId),
		Name:      tagValue(gateway.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    status,
		Fields: []model.Field{
			{Key: "VpcId", Value: stringSliceOrDash(vpcIDs)},
			{Key: "AttachmentState", Value: status},
			{Key: "OwnerId", Value: orDash(aws.ToString(gateway.OwnerId))},
		},
		Tags:    ec2Tags(gateway.Tags),
		Related: refs,
	}
}

func stringSliceOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}

	return strings.Join(values, ", ")
}
