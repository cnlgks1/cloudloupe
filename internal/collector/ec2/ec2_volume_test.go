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

// fakeVolumesAPI는 describeVolumesAPI를 만족하는 테스트 대역이다. 실제 AWS를 호출하지
// 않고 미리 준비한 페이지를 순서대로 돌려준다.
type fakeVolumesAPI struct {
	pages []*awsec2.DescribeVolumesOutput
	err   error
	calls int
}

func (f *fakeVolumesAPI) DescribeVolumes(_ context.Context, _ *awsec2.DescribeVolumesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeVolumesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}

	page := f.pages[f.calls]
	f.calls++

	return page, nil
}

func TestEC2VolumeCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, time.March, 11, 2, 51, 19, 0, time.UTC)

	api := &fakeVolumesAPI{pages: []*awsec2.DescribeVolumesOutput{{
		Volumes: []ec2types.Volume{{
			VolumeId:         aws.String("vol-0123456789abcdef0"),
			VolumeType:       ec2types.VolumeTypeGp3,
			State:            ec2types.VolumeStateInUse,
			Size:             aws.Int32(100),
			Iops:             aws.Int32(3000),
			AvailabilityZone: aws.String("ap-northeast-2a"),
			Encrypted:        aws.Bool(true),
			CreateTime:       aws.Time(created),
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("web-data")},
			},
			Attachments: []ec2types.VolumeAttachment{
				{InstanceId: aws.String("i-0a1b2c3d4e5f60718"), Device: aws.String("/dev/xvda")},
			},
		}},
	}}}

	c := ec2.NewVolume(api)

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

	if r.Type != model.TypeEC2Volume {
		t.Errorf("Type = %q, want %q", r.Type, model.TypeEC2Volume)
	}

	if r.ID != "vol-0123456789abcdef0" {
		t.Errorf("ID = %q", r.ID)
	}

	if r.Name != "web-data" {
		t.Errorf("Name = %q, want web-data (Name 태그에서)", r.Name)
	}

	if r.Status != "in-use" {
		t.Errorf("Status = %q, want in-use", r.Status)
	}

	if r.CreatedAt == nil || !r.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", r.CreatedAt, created)
	}

	if got := r.FieldValue("타입"); got != "gp3" {
		t.Errorf("타입 = %q, want gp3", got)
	}

	if got := r.FieldValue("크기(GiB)"); got != "100" {
		t.Errorf("크기 = %q, want 100", got)
	}

	if got := r.FieldValue("암호화"); got != "예" {
		t.Errorf("암호화 = %q, want 예", got)
	}
}

func TestEC2VolumeCollectorRecordsAttachment(t *testing.T) {
	t.Parallel()

	api := &fakeVolumesAPI{pages: []*awsec2.DescribeVolumesOutput{{
		Volumes: []ec2types.Volume{{
			VolumeId: aws.String("vol-1"),
			State:    ec2types.VolumeStateInUse,
			Attachments: []ec2types.VolumeAttachment{
				{InstanceId: aws.String("i-1"), Device: aws.String("/dev/sdf")},
			},
		}},
	}}}

	c := ec2.NewVolume(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// 관계를 양쪽 끝에서 기록: 볼륨은 자신이 붙은 인스턴스로의 attached-to를 남긴다.
	att := got[0].RelatedBy(model.RelationAttachedTo)
	if len(att) != 1 || att[0].ID != "i-1" || att[0].Via != "/dev/sdf" {
		t.Errorf("인스턴스 attached-to 관계가 없거나 디바이스가 빠졌다: %+v", got[0].Related)
	}
}

func TestEC2VolumeCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeVolumesAPI{pages: []*awsec2.DescribeVolumesOutput{
		{
			Volumes:   []ec2types.Volume{{VolumeId: aws.String("vol-1"), State: ec2types.VolumeStateAvailable}},
			NextToken: aws.String("page2"),
		},
		{
			Volumes: []ec2types.Volume{{VolumeId: aws.String("vol-2"), State: ec2types.VolumeStateAvailable}},
		},
	}}

	c := ec2.NewVolume(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("두 페이지에서 볼륨 2개가 나와야 한다, got %d", len(got))
	}

	if api.calls != 2 {
		t.Errorf("페이지네이터가 %d번 호출됨, want 2", api.calls)
	}
}

func TestEC2VolumeCollectorWrapsError(t *testing.T) {
	t.Parallel()

	api := &fakeVolumesAPI{err: errors.New("UnauthorizedOperation")}
	c := ec2.NewVolume(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("에러가 반환되어야 한다")
	}

	if got := err.Error(); got == "UnauthorizedOperation" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestEC2VolumeCollectorType(t *testing.T) {
	t.Parallel()

	c := ec2.NewVolume(&fakeVolumesAPI{})
	if c.Type() != model.TypeEC2Volume {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeEC2Volume)
	}
}
