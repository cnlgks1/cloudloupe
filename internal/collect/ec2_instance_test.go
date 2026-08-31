package collect_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeEC2는 describeInstancesAPI를 만족하는 테스트 대역이다.
//
// 좁은 인터페이스 덕분에 *ec2.Client 전체를 흉내 낼 필요 없이 메서드 하나만 구현하면
// 된다. 실제 AWS를 절대 호출하지 않는다.
type fakeEC2 struct {
	pages []*ec2.DescribeInstancesOutput
	err   error
	calls int
}

func (f *fakeEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}

	// 페이지네이터는 NextToken이 있는 동안 계속 호출한다. 페이지를 순서대로 돌려준다.
	page := f.pages[f.calls]
	f.calls++

	return page, nil
}

func TestEC2InstanceCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	launch := time.Date(2025, time.March, 11, 2, 51, 19, 0, time.UTC)

	api := &fakeEC2{pages: []*ec2.DescribeInstancesOutput{{
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

	c := collect.NewEC2InstanceCollector(api)

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

	if got := r.FieldValue("인스턴스 타입"); got != "t3.medium" {
		t.Errorf("인스턴스 타입 = %q", got)
	}
}

func TestEC2InstanceCollectorRecordsRelations(t *testing.T) {
	t.Parallel()

	api := &fakeEC2{pages: []*ec2.DescribeInstancesOutput{{
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

	c := collect.NewEC2InstanceCollector(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// 관계를 양쪽 끝에서 기록하는 원칙: 인스턴스는 ENI와 볼륨으로의 관계를 남겨야 한다.
	// 3단계 그래프가 이 ref로부터 만들어진다.
	eni := got[0].RelatedBy(model.RelationAttachedENI)
	if len(eni) != 1 || eni[0].ID != "eni-1" {
		t.Errorf("ENI 관계가 없다: %+v", got[0].Related)
	}

	vol := got[0].RelatedBy(model.RelationAttachedVolume)
	if len(vol) != 1 || vol[0].ID != "vol-1" || vol[0].Via != "/dev/xvda" {
		t.Errorf("볼륨 관계가 없거나 디바이스가 빠졌다: %+v", got[0].Related)
	}
}

func TestEC2InstanceCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	// 페이지가 여러 개면 전부 따라가야 한다. NextToken이 있으면 페이지네이터가 다시
	// 호출한다.
	api := &fakeEC2{pages: []*ec2.DescribeInstancesOutput{
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

	c := collect.NewEC2InstanceCollector(api)

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
	c := collect.NewEC2InstanceCollector(api)

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

	c := collect.NewEC2InstanceCollector(&fakeEC2{})
	if c.Type() != model.TypeEC2Instance {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeEC2Instance)
	}
}
