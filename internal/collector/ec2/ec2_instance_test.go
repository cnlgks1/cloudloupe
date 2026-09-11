package ec2_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeEC2는 describeInstancesAPI를 만족하는 테스트 대역이다.
//
// 좁은 인터페이스 덕분에 *awsec2.Client 전체를 흉내 낼 필요 없이 메서드 하나만 구현하면
// 된다. 실제 AWS를 절대 호출하지 않는다.
type fakeEC2 struct {
	pages []*awsec2.DescribeInstancesOutput
	err   error
	calls int

	// volumes는 DescribeVolumes가 돌려줄 볼륨(VolumeId → Size)이다. volumesErr가 있으면
	// 볼륨 조회 실패를 흉내 낸다.
	volumes    map[string]int32
	volumesErr error
	volCalls   int
}

func (f *fakeEC2) DescribeInstances(_ context.Context, _ *awsec2.DescribeInstancesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}

	// 페이지네이터는 NextToken이 있는 동안 계속 호출한다. 페이지를 순서대로 돌려준다.
	page := f.pages[f.calls]
	f.calls++

	return page, nil
}

func (f *fakeEC2) DescribeVolumes(_ context.Context, in *awsec2.DescribeVolumesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeVolumesOutput, error) {
	f.volCalls++
	if f.volumesErr != nil {
		return nil, f.volumesErr
	}

	out := &awsec2.DescribeVolumesOutput{}
	for _, id := range in.VolumeIds {
		if size, ok := f.volumes[id]; ok {
			out.Volumes = append(out.Volumes, ec2types.Volume{
				VolumeId: aws.String(id),
				Size:     aws.Int32(size),
			})
		}
	}

	return out, nil
}

func TestEC2InstanceCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	launch := time.Date(2025, time.March, 11, 2, 51, 19, 0, time.UTC)

	api := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:       aws.String("i-0a1b2c3d4e5f60718"),
				InstanceType:     ec2types.InstanceTypeT3Medium,
				State:            &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
				PrivateIpAddress: aws.String("10.0.1.24"),
				VpcId:            aws.String("vpc-0aa11bb22cc33dd44"),
				SubnetId:         aws.String("subnet-0a1b2c3d4e5f60718"),
				ImageId:          aws.String("ami-0abcdef1234567890"),
				KeyName:          aws.String("web-prod"),
				LaunchTime:       aws.Time(launch),
				Placement:        &ec2types.Placement{AvailabilityZone: aws.String("ap-northeast-2a")},
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String("web-prod-01")},
					{Key: aws.String("Environment"), Value: aws.String("production")},
				},
				NetworkInterfaces: []ec2types.InstanceNetworkInterface{
					{NetworkInterfaceId: aws.String("eni-0aa11bb22cc33dd44")},
				},
				BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{
					{DeviceName: aws.String("/dev/xvda"), Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-0123456789abcdef0")}},
				},
			}},
		}},
		// NextToken 없음 → 페이지 하나로 끝.
	}}}
	api.volumes = map[string]int32{"vol-0123456789abcdef0": 30}

	c := ec2.NewInstance(api)

	got, err := c.Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	r := got[0]

	if r.Type != model.TypeEC2Instance {
		t.Errorf("Type = %q, want %q", r.Type, model.TypeEC2Instance)
	}

	if r.ID != "i-0a1b2c3d4e5f60718" {
		t.Errorf("ID = %q", r.ID)
	}

	if r.Name != "web-prod-01" {
		t.Errorf("Name = %q, want web-prod-01 (Name 태그에서)", r.Name)
	}

	if r.Status != "running" {
		t.Errorf("Status = %q, want running", r.Status)
	}

	if r.Region != "ap-northeast-2" || r.Profile != "prod" || r.AccountID != "123456789012" {
		t.Errorf("범위 정보가 안 붙었다: region=%q profile=%q account=%q", r.Region, r.Profile, r.AccountID)
	}

	if r.CreatedAt == nil || !r.CreatedAt.Equal(launch) {
		t.Errorf("CreatedAt = %v, want %v", r.CreatedAt, launch)
	}

	// 태그는 키 순으로 정렬되어야 한다: Environment 먼저, Name 나중.
	if len(r.Tags) != 2 || r.Tags[0].Key != "Environment" || r.Tags[1].Key != "Name" {
		t.Errorf("태그 정렬이 안 됐다: %+v", r.Tags)
	}

	if got := r.FieldValue("InstanceType"); got != "t3.medium" {
		t.Errorf("인스턴스 타입 = %q", got)
	}

	// EBS 볼륨 개수와 총 용량이 DescribeVolumes 결과로 채워져야 한다.
	if got := r.FieldValue("EbsVolumeCount"); got != "1" {
		t.Errorf("EbsVolumeCount = %q, want 1", got)
	}
	if got := r.FieldValue("EbsTotalSizeGiB"); got != "30" {
		t.Errorf("EbsTotalSizeGiB = %q, want 30", got)
	}
	if api.volCalls != 1 {
		t.Errorf("DescribeVolumes 호출 = %d회, want 1 (붙은 볼륨을 한 번에 조회)", api.volCalls)
	}
}

func TestEC2InstanceCollectorRecordsRelations(t *testing.T) {
	t.Parallel()

	api := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId: aws.String("i-1"),
				State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
				NetworkInterfaces: []ec2types.InstanceNetworkInterface{
					{NetworkInterfaceId: aws.String("eni-1")},
				},
				BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{
					{DeviceName: aws.String("/dev/xvda"), Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-1")}},
				},
			}},
		}},
	}}}
	api.volumes = map[string]int32{"vol-1": 100}

	c := ec2.NewInstance(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// 관계를 양쪽 끝에서 기록하는 원칙: 인스턴스는 ENI와 볼륨으로의 관계를 남겨야 한다.
	// 3단계 그래프가 이 ref로부터 만들어진다.
	eni := got[0].RelatedBy("NetworkInterfaces.NetworkInterfaceId")
	if len(eni) != 1 || eni[0].ID != "eni-1" {
		t.Errorf("ENI 관계가 없다: %+v", got[0].Related)
	}

	// 볼륨 관계의 Via에는 디바이스명과 함께 용량이 붙어야 한다. 볼륨을 따로 조회하지 않아도
	// 관계 줄에서 각 볼륨 용량을 읽게 하려는 것이다.
	vol := got[0].RelatedBy("BlockDeviceMappings.Ebs.VolumeId")
	if len(vol) != 1 || vol[0].ID != "vol-1" || vol[0].Via != "/dev/xvda (100 GiB)" {
		t.Errorf("볼륨 관계에 디바이스·용량이 안 붙었다: %+v", got[0].Related)
	}
}

// TestEC2InstanceRelationOmitsSizeWhenVolumeUnknown은 볼륨 용량을 조회하지 못하면 관계
// Via에 디바이스명만 남기는지 확인한다(용량 괄호 없음).
func TestEC2InstanceRelationOmitsSizeWhenVolumeUnknown(t *testing.T) {
	t.Parallel()

	api := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{
			InstanceId: aws.String("i-1"),
			State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{
				{DeviceName: aws.String("/dev/xvda"), Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-1")}},
			},
		}}}},
	}}}
	api.volumesErr = errors.New("AccessDenied") // 볼륨 조회 실패 → 용량 모름

	got, err := ec2.NewInstance(api).Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("볼륨 조회 실패가 부분 오류로 반환되어야 한다")
	}
	vol := got[0].RelatedBy("BlockDeviceMappings.Ebs.VolumeId")
	if len(vol) != 1 || vol[0].Via != "/dev/xvda" {
		t.Errorf("용량을 모르면 디바이스명만 남아야 한다: %+v", got[0].Related)
	}
}

func TestEC2InstanceCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	// 페이지가 여러 개면 전부 따라가야 한다. NextToken이 있으면 페이지네이터가 다시
	// 호출한다.
	api := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{
		{
			Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
				{InstanceId: aws.String("i-1"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			}}},
			NextToken: aws.String("page2"),
		},
		{
			Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
				{InstanceId: aws.String("i-2"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			}}},
		},
	}}

	c := ec2.NewInstance(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("두 페이지에서 인스턴스 2개가 나와야 한다, got %d", len(got))
	}

	if api.calls != 2 {
		t.Errorf("페이지네이터가 %d번 호출됨, want 2", api.calls)
	}
}

func TestEC2InstanceCollectorWrapsError(t *testing.T) {
	t.Parallel()

	api := &fakeEC2{err: errors.New("UnauthorizedOperation")}
	c := ec2.NewInstance(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("에러가 반환되어야 한다")
	}

	// 에러에 문맥이 붙어야 한다: 어느 작업에서 실패했는지.
	if got := err.Error(); got == "UnauthorizedOperation" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestEC2InstanceCollectorType(t *testing.T) {
	t.Parallel()

	c := ec2.NewInstance(&fakeEC2{})
	if c.Type() != model.TypeEC2Instance {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeEC2Instance)
	}
}

// TestEC2InstanceCollectorVolumeLookupPartialFailure는 볼륨 용량 조회가 실패해도 인스턴스
// 목록은 반환하고 용량만 비우는지 확인한다. 부분 실패 허용 원칙이다: 추가 조회 하나가
// 실패했다고 이미 얻은 인스턴스를 통째로 버리지 않는다.
func TestEC2InstanceCollectorVolumeLookupPartialFailure(t *testing.T) {
	t.Parallel()

	api := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{
			InstanceId: aws.String("i-1"),
			State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{
				{DeviceName: aws.String("/dev/xvda"), Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String("vol-1")}},
			},
		}}}},
	}}}
	api.volumesErr = errors.New("AccessDenied")

	c := ec2.NewInstance(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("볼륨 조회 실패가 부분 오류로 반환되어야 한다")
	}
	if len(got) != 1 {
		t.Fatalf("인스턴스는 그대로 반환되어야 한다, got %d", len(got))
	}
	if v := got[0].FieldValue("EbsTotalSizeGiB"); v != "-" {
		t.Errorf("볼륨 조회 실패 시 총 용량은 -여야 한다, got %q", v)
	}
	// 개수는 인스턴스 응답만으로 셀 수 있으므로 그대로 나온다.
	if v := got[0].FieldValue("EbsVolumeCount"); v != "1" {
		t.Errorf("EbsVolumeCount = %q, want 1", v)
	}
}

// TestEC2InstanceCollectorHandlesNilState는 State가 없는 인스턴스도 패닉 없이 변환하는지
// 확인한다. State는 SDK에서 포인터라 이론상 nil일 수 있다.
func TestEC2InstanceCollectorHandlesNilState(t *testing.T) {
	t.Parallel()

	api := &fakeEC2{pages: []*awsec2.DescribeInstancesOutput{{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{
			InstanceId: aws.String("i-nostate"),
			// State 없음(nil).
		}}}},
	}}}

	got, err := ec2.NewInstance(api).Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0].ID != "i-nostate" {
		t.Fatalf("인스턴스가 변환되지 않았다: %+v", got)
	}
	if got[0].Status != "" {
		t.Errorf("State가 없으면 상태는 빈 문자열이어야 한다, got %q", got[0].Status)
	}
}
