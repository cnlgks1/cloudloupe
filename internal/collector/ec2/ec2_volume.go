package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeVolumesAPI는 EBS 볼륨 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ec2_instance.go의 describeInstancesAPI와 같은 규율이다. *awsec2.Client 전체가 아니라
// 이 한 메서드만 받아, 조회 하나만 쓴다는 것을 타입에 드러내고 자격증명 없이 fake로
// 테스트할 수 있게 한다. 메서드 이름이 Describe로 시작하므로 조회 전용 가드를 통과한다.
type describeVolumesAPI interface {
	DescribeVolumes(context.Context, *awsec2.DescribeVolumesInput, ...func(*awsec2.Options)) (*awsec2.DescribeVolumesOutput, error)
}

// ec2VolumeCollector는 EBS 볼륨을 조회한다.
type ec2VolumeCollector struct {
	api describeVolumesAPI
}

// NewVolume은 볼륨 수집기를 만든다.
func NewVolume(api describeVolumesAPI) collect.Collector {
	return ec2VolumeCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c ec2VolumeCollector) Type() string { return model.TypeEC2Volume }

// Collect는 범위 안의 EBS 볼륨을 모두 조회해 도메인 리소스로 변환한다.
func (c ec2VolumeCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeVolumesPaginator(c.api, &awsec2.DescribeVolumesInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe volumes: %w", err)
		}

		for i := range page.Volumes {
			out = append(out, volumeToResource(req.Scope, page.Volumes[i]))
		}
	}

	return out, nil
}

// volumeToResource는 SDK 볼륨을 도메인 리소스로 변환한다.
func volumeToResource(scope collect.Scope, vol ec2types.Volume) model.Resource {
	r := model.Resource{
		Type:      model.TypeEC2Volume,
		ID:        aws.ToString(vol.VolumeId),
		Name:      tagValue(vol.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(vol.State),
		Tags:      ec2Tags(vol.Tags),
	}

	if vol.CreateTime != nil {
		t := vol.CreateTime.UTC()
		r.CreatedAt = &t
	}

	r.Fields = []model.Field{
		{Key: "VolumeType", Value: string(vol.VolumeType)},
		{Key: "Size", Value: itoa32(aws.ToInt32(vol.Size))},
		{Key: "Iops", Value: itoa32(aws.ToInt32(vol.Iops))},
		{Key: "AvailabilityZone", Value: orDash(aws.ToString(vol.AvailabilityZone))},
		{Key: "Encrypted", Value: boolValue(aws.ToBool(vol.Encrypted))},
	}

	r.Related = volumeRelations(vol)

	return r
}

// volumeRelations는 볼륨이 붙어 있는 인스턴스로의 관계를 만든다.
//
// 관계를 양쪽 끝에서 기록하는 원칙에 따라, 볼륨 → 인스턴스 방향(attached-to)을 남긴다.
// 인스턴스 → 볼륨(attached-volume)은 ec2_instance.go에서 남긴다. 3단계 그래프에서
// 둘이 합쳐진다.
func volumeRelations(vol ec2types.Volume) []model.Ref {
	var refs []model.Ref

	for _, att := range vol.Attachments {
		if id := aws.ToString(att.InstanceId); id != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeEC2Instance,
				ID:       id,
				Relation: model.RelationAttachedTo,
				Via:      aws.ToString(att.Device),
			})
		}
	}

	return refs
}
