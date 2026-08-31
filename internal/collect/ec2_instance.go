package collect

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeInstancesAPI는 EC2 인스턴스 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// *ec2.Client 전체가 아니라 이 한 메서드만 받는다("accept interfaces, return structs").
// 두 가지 효과가 있다. 첫째, 이 수집기가 조회 메서드 하나만 쓴다는 것이 타입에 드러난다.
// 둘째, 자격증명 없이 fake로 테스트할 수 있다. *ec2.Client가 이 인터페이스를 자동으로
// 만족한다.
//
// 메서드 이름이 Describe로 시작하므로 조회 전용 가드(scripts/verify-readonly.sh)를
// 통과한다.
type describeInstancesAPI interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// ec2InstanceCollector는 EC2 인스턴스를 조회한다.
type ec2InstanceCollector struct {
	api describeInstancesAPI
}

// NewEC2InstanceCollector는 인스턴스 수집기를 만든다.
func NewEC2InstanceCollector(api describeInstancesAPI) Collector {
	return ec2InstanceCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c ec2InstanceCollector) Type() string { return model.TypeEC2Instance }

// Collect는 범위 안의 EC2 인스턴스를 모두 조회해 도메인 리소스로 변환한다.
//
// SDK 페이지네이터를 쓴다. 토큰 루프를 손으로 돌리지 않는다. ctx는 페이지마다 검사되어
// 중간에 취소하면 즉시 멈춘다.
func (c ec2InstanceCollector) Collect(ctx context.Context, req Request) ([]model.Resource, error) {
	paginator := ec2.NewDescribeInstancesPaginator(c.api, &ec2.DescribeInstancesInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe instances: %w", err)
		}

		// 응답은 예약(Reservation) 단위로 묶여 오고 그 안에 인스턴스가 들어 있다.
		for i := range page.Reservations {
			for j := range page.Reservations[i].Instances {
				out = append(out, instanceToResource(req.Scope, page.Reservations[i].Instances[j]))
			}
		}
	}

	return out, nil
}

// instanceToResource는 SDK 인스턴스를 도메인 리소스로 변환한다.
//
// SDK의 포인터 값은 여기 경계에서 값으로 바꾼다(aws.ToString 등). 포인터가 도메인
// 모델 안까지 들어오면 nil 체크가 전염된다.
func instanceToResource(scope Scope, inst ec2types.Instance) model.Resource {
	id := aws.ToString(inst.InstanceId)

	// ARN은 채우지 않는다. 인스턴스 describe 응답에 ARN이 없고, 문자열로 조립할 수는
	// 있지만 아직 쓰는 곳이 없다. 필요해지면 그때 만든다("약간의 복사가 약간의 의존보다
	// 낫다"의 반대 방향 — 쓰지 않는 것을 미리 만들지 않는다).
	r := model.Resource{
		Type:      model.TypeEC2Instance,
		ID:        id,
		Name:      tagValue(inst.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(inst.State.Name),
		Tags:      ec2Tags(inst.Tags),
	}

	if inst.LaunchTime != nil {
		t := inst.LaunchTime.UTC()
		r.CreatedAt = &t
	}

	r.Fields = []model.Field{
		{Key: "인스턴스 타입", Value: string(inst.InstanceType)},
		{Key: "가용 영역", Value: azOf(inst)},
		{Key: "사설 IP", Value: orDash(aws.ToString(inst.PrivateIpAddress))},
		{Key: "공인 IP", Value: orDash(aws.ToString(inst.PublicIpAddress))},
		{Key: "VPC", Value: orDash(aws.ToString(inst.VpcId))},
		{Key: "서브넷", Value: orDash(aws.ToString(inst.SubnetId))},
		{Key: "AMI", Value: orDash(aws.ToString(inst.ImageId))},
		{Key: "키 페어", Value: orDash(aws.ToString(inst.KeyName))},
	}

	r.Related = instanceRelations(inst)

	return r
}

// instanceRelations는 인스턴스가 가리키는 다른 리소스로의 관계를 만든다.
//
// 관계를 양쪽 끝에서 기록하는 원칙에 따라, 여기서 인스턴스 → ENI/볼륨 방향을 남긴다.
// 3단계 그래프 작업에서 반대 방향과 합쳐진다.
func instanceRelations(inst ec2types.Instance) []model.Ref {
	var refs []model.Ref

	for _, ni := range inst.NetworkInterfaces {
		if id := aws.ToString(ni.NetworkInterfaceId); id != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeEC2NetworkInterface,
				ID:       id,
				Relation: model.RelationAttachedENI,
			})
		}
	}

	for _, bd := range inst.BlockDeviceMappings {
		if bd.Ebs == nil {
			continue
		}

		if id := aws.ToString(bd.Ebs.VolumeId); id != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeEC2Volume,
				ID:       id,
				Relation: model.RelationAttachedVolume,
				Via:      aws.ToString(bd.DeviceName),
			})
		}
	}

	return refs
}

func azOf(inst ec2types.Instance) string {
	if inst.Placement == nil {
		return "-"
	}

	return orDash(aws.ToString(inst.Placement.AvailabilityZone))
}

// ec2Tags는 SDK 태그 슬라이스를 정렬된 도메인 필드로 바꾼다.
func ec2Tags(tags []ec2types.Tag) []model.Field {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return model.TagFields(m)
}

// tagValue는 SDK 태그 슬라이스에서 특정 키의 값을 찾는다.
func tagValue(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}

	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
