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

// describeInstancesAPI는 EC2 인스턴스 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// *awsec2.Client 전체가 아니라 이 한 메서드만 받는다("accept interfaces, return structs").
// 두 가지 효과가 있다. 첫째, 이 수집기가 조회 메서드 하나만 쓴다는 것이 타입에 드러난다.
// 둘째, 자격증명 없이 fake로 테스트할 수 있다. *awsec2.Client가 이 인터페이스를 자동으로
// 만족한다.
//
// 메서드 이름이 Describe로 시작하므로 조회 전용 가드(scripts/verify-readonly.sh)를
// 통과한다.
type describeInstancesAPI interface {
	DescribeInstances(context.Context, *awsec2.DescribeInstancesInput, ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error)
	// DescribeVolumes로 인스턴스에 붙은 EBS 볼륨의 용량을 함께 가져온다. 인스턴스 응답
	// (BlockDeviceMappings.Ebs)에는 VolumeId만 있고 용량은 없기 때문이다. 콘솔이
	// 인스턴스 상세에 스토리지 용량을 보여주는 것도 같은 추가 조회를 하기 때문이다.
	// 이름이 Describe로 시작하므로 조회 전용 가드를 통과한다.
	DescribeVolumes(context.Context, *awsec2.DescribeVolumesInput, ...func(*awsec2.Options)) (*awsec2.DescribeVolumesOutput, error)
}

// ec2InstanceCollector는 EC2 인스턴스를 조회한다.
type ec2InstanceCollector struct {
	api describeInstancesAPI
}

// NewInstance는 인스턴스 수집기를 만든다.
func NewInstance(api describeInstancesAPI) collect.Collector {
	return ec2InstanceCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c ec2InstanceCollector) Type() string { return model.TypeEC2Instance }

// Collect는 범위 안의 EC2 인스턴스를 모두 조회해 도메인 리소스로 변환한다.
//
// SDK 페이지네이터를 쓴다. 토큰 루프를 손으로 돌리지 않는다. ctx는 페이지마다 검사되어
// 중간에 취소하면 즉시 멈춘다.
func (c ec2InstanceCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeInstancesPaginator(c.api, &awsec2.DescribeInstancesInput{})

	var instances []ec2types.Instance

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe instances: %w", err)
		}

		// 응답은 예약(Reservation) 단위로 묶여 오고 그 안에 인스턴스가 들어 있다.
		for i := range page.Reservations {
			instances = append(instances, page.Reservations[i].Instances...)
		}
	}

	// 인스턴스에 붙은 EBS 볼륨의 용량을 함께 가져온다. 인스턴스 응답에는 VolumeId만 있어
	// 용량은 DescribeVolumes로 따로 조회해야 한다. 모든 인스턴스의 볼륨 ID를 모아 한 번에
	// 조회해 호출 수를 줄인다. 볼륨 조회가 실패해도 인스턴스 목록은 반환하고, 용량만 비운다
	// ("부분 실패 허용"). 실패는 상위에서 부분 오류로 보고되도록 함께 돌려준다.
	sizes, volErr := c.volumeSizes(ctx, instances)

	out := make([]model.Resource, 0, len(instances))
	for i := range instances {
		out = append(out, instanceToResource(req.Scope, instances[i], sizes))
	}

	if volErr != nil {
		return out, volErr
	}

	return out, nil
}

// volumeSizes는 인스턴스들에 붙은 EBS 볼륨 ID를 모아 용량(GiB)을 조회해 맵으로 만든다.
//
// 볼륨 ID가 하나도 없으면 조회하지 않는다. DescribeVolumes를 VolumeId 필터로 한 번에
// 호출하고, 페이지네이터로 결과를 모은다.
func (c ec2InstanceCollector) volumeSizes(
	ctx context.Context,
	instances []ec2types.Instance,
) (map[string]int32, error) {
	var ids []string
	seen := make(map[string]struct{})
	for i := range instances {
		for _, bd := range instances[i].BlockDeviceMappings {
			if bd.Ebs == nil {
				continue
			}
			id := aws.ToString(bd.Ebs.VolumeId)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		return nil, nil
	}

	sizes := make(map[string]int32, len(ids))
	paginator := awsec2.NewDescribeVolumesPaginator(c.api, &awsec2.DescribeVolumesInput{
		VolumeIds: ids,
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return sizes, fmt.Errorf("describe volumes page: %w", err)
		}
		for _, vol := range page.Volumes {
			sizes[aws.ToString(vol.VolumeId)] = aws.ToInt32(vol.Size)
		}
	}

	return sizes, nil
}

// instanceToResource는 SDK 인스턴스를 도메인 리소스로 변환한다.
//
// SDK의 포인터 값은 여기 경계에서 값으로 바꾼다(aws.ToString 등). 포인터가 도메인
// 모델 안까지 들어오면 nil 체크가 전염된다.
func instanceToResource(scope collect.Scope, inst ec2types.Instance, volumeSizes map[string]int32) model.Resource {
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
		Status:    instanceState(inst),
		Tags:      ec2Tags(inst.Tags),
	}

	if inst.LaunchTime != nil {
		t := inst.LaunchTime.UTC()
		r.CreatedAt = &t
	}

	r.Fields = []model.Field{
		{Key: "InstanceType", Value: string(inst.InstanceType)},
		{Key: "AvailabilityZone", Value: azOf(inst)},
		{Key: "PrivateIpAddress", Value: orDash(aws.ToString(inst.PrivateIpAddress))},
		{Key: "PublicIpAddress", Value: orDash(aws.ToString(inst.PublicIpAddress))},
		{Key: "EbsVolumeCount", Value: itoa32(ebsVolumeCount(inst))},
		{Key: "EbsTotalSizeGiB", Value: ebsTotalSize(inst, volumeSizes)},
		{Key: "VpcId", Value: orDash(aws.ToString(inst.VpcId))},
		{Key: "SubnetId", Value: orDash(aws.ToString(inst.SubnetId))},
		{Key: "ImageId", Value: orDash(aws.ToString(inst.ImageId))},
		{Key: "KeyName", Value: orDash(aws.ToString(inst.KeyName))},
	}

	r.Related = instanceRelations(inst, volumeSizes)

	return r
}

// ebsVolumeCount는 인스턴스에 붙은 EBS 볼륨 수를 센다.
func ebsVolumeCount(inst ec2types.Instance) int32 {
	var count int32
	for _, bd := range inst.BlockDeviceMappings {
		if bd.Ebs != nil && aws.ToString(bd.Ebs.VolumeId) != "" {
			count++
		}
	}

	return count
}

// ebsTotalSize는 인스턴스에 붙은 EBS 볼륨 용량의 합(GiB)을 문자열로 반환한다.
//
// 용량은 DescribeVolumes로 미리 조회한 sizes 맵에서 찾는다. 볼륨 조회가 실패했거나 붙은
// 볼륨이 없으면 "-"를 반환한다. 여러 볼륨이면 합계를 보여준다. 콘솔의 인스턴스 스토리지
// 요약과 같은 값이다.
func ebsTotalSize(inst ec2types.Instance, sizes map[string]int32) string {
	var total int32
	found := false
	for _, bd := range inst.BlockDeviceMappings {
		if bd.Ebs == nil {
			continue
		}
		id := aws.ToString(bd.Ebs.VolumeId)
		if size, ok := sizes[id]; ok {
			total += size
			found = true
		}
	}

	if !found {
		return "-"
	}

	return itoa32(total)
}

// instanceRelations는 인스턴스가 가리키는 다른 리소스로의 관계를 만든다.
//
// 관계를 양쪽 끝에서 기록하는 원칙에 따라, 여기서 인스턴스 → ENI/볼륨 방향을 남긴다.
// 3단계 그래프 작업에서 반대 방향과 합쳐진다.
func instanceRelations(inst ec2types.Instance, volumeSizes map[string]int32) []model.Ref {
	var refs []model.Ref

	for _, ni := range inst.NetworkInterfaces {
		if id := aws.ToString(ni.NetworkInterfaceId); id != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeEC2NetworkInterface,
				ID:       id,
				Relation: "NetworkInterfaces.NetworkInterfaceId",
			})
		}
	}

	for _, bd := range inst.BlockDeviceMappings {
		if bd.Ebs == nil {
			continue
		}

		if id := aws.ToString(bd.Ebs.VolumeId); id != "" {
			// Via에 디바이스명과 함께 볼륨 용량을 붙인다. 볼륨을 따로 조회하지 않아도
			// 인스턴스 상세의 관계 줄에서 각 볼륨의 용량을 바로 읽게 한다. 용량은 인스턴스
			// 수집 때 함께 조회한 DescribeVolumes 결과에서 온다. 조회하지 못한 볼륨은
			// 디바이스명만 남긴다.
			via := aws.ToString(bd.DeviceName)
			if size, ok := volumeSizes[id]; ok {
				via = strings.TrimSpace(via + " (" + itoa32(size) + " GiB)")
			}

			refs = append(refs, model.Ref{
				Type:     model.TypeEC2Volume,
				ID:       id,
				Relation: "BlockDeviceMappings.Ebs.VolumeId",
				Via:      via,
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

// instanceState는 인스턴스 상태를 반환한다. State는 포인터라 nil일 수 있으므로
// azOf와 같은 규율로 방어한다. 다른 수집기(elbv2 등)도 상태 포인터를 nil 체크한다.
func instanceState(inst ec2types.Instance) string {
	if inst.State == nil {
		return ""
	}

	return string(inst.State.Name)
}
