package awsclient

// commercialRegions는 AWS 상용 파티션의 리전 목록이다.
//
// 정적 목록을 쓰는 이유: 진짜 리전 목록을 얻는 DescribeRegions는 자격증명과 이미 정해진
// 리전이 필요하다. 그런데 이 목록이 필요한 시점은 사용자가 아직 리전을 고르기 전이다.
// 닭과 달걀이다. 그래서 리전 선택 화면에는 정적 목록을 보여주고, 실제 조회는 사용자가
// 고른 리전으로 한다. 없는 리전을 골라도 조회 단계에서 명확한 에러가 나고 Explain이
// 설명한다.
//
// GovCloud(us-gov-*)와 중국(cn-*)은 뺐다. 별도 파티션이고 별도 자격증명·엔드포인트가
// 필요하며, 이 목록에 섞으면 대부분의 사용자에게 노이즈다. 필요해지면 파티션 개념을
// 제대로 도입한다.
func commercialRegions() []Region {
	return []Region{
		{Code: "us-east-1", Name: "US East (N. Virginia)"},
		{Code: "us-east-2", Name: "US East (Ohio)"},
		{Code: "us-west-1", Name: "US West (N. California)"},
		{Code: "us-west-2", Name: "US West (Oregon)"},
		{Code: "af-south-1", Name: "Africa (Cape Town)"},
		{Code: "ap-east-1", Name: "Asia Pacific (Hong Kong)"},
		{Code: "ap-south-1", Name: "Asia Pacific (Mumbai)"},
		{Code: "ap-south-2", Name: "Asia Pacific (Hyderabad)"},
		{Code: "ap-southeast-1", Name: "Asia Pacific (Singapore)"},
		{Code: "ap-southeast-2", Name: "Asia Pacific (Sydney)"},
		{Code: "ap-southeast-3", Name: "Asia Pacific (Jakarta)"},
		{Code: "ap-northeast-1", Name: "Asia Pacific (Tokyo)"},
		{Code: "ap-northeast-2", Name: "Asia Pacific (Seoul)"},
		{Code: "ap-northeast-3", Name: "Asia Pacific (Osaka)"},
		{Code: "ca-central-1", Name: "Canada (Central)"},
		{Code: "eu-central-1", Name: "Europe (Frankfurt)"},
		{Code: "eu-central-2", Name: "Europe (Zurich)"},
		{Code: "eu-west-1", Name: "Europe (Ireland)"},
		{Code: "eu-west-2", Name: "Europe (London)"},
		{Code: "eu-west-3", Name: "Europe (Paris)"},
		{Code: "eu-north-1", Name: "Europe (Stockholm)"},
		{Code: "eu-south-1", Name: "Europe (Milan)"},
		{Code: "eu-south-2", Name: "Europe (Spain)"},
		{Code: "me-south-1", Name: "Middle East (Bahrain)"},
		{Code: "me-central-1", Name: "Middle East (UAE)"},
		{Code: "sa-east-1", Name: "South America (Sao Paulo)"},
	}
}

// Region은 리전 코드와 사람이 읽는 이름이다.
type Region struct {
	Code string
	Name string
}

// Regions는 리전 선택 화면에 보여줄 목록을 반환한다.
//
// defaultRegion(프로필이나 환경에서 온 기본 리전)이 목록에 있으면 맨 앞으로 올린다.
// 사용자는 대개 자기 기본 리전부터 보고 싶어 하기 때문이다. 목록에 없는 기본 리전도
// (커스텀 엔드포인트 등) 맨 앞에 추가한다.
func Regions(defaultRegion string) []Region {
	regions := commercialRegions()
	out := make([]Region, 0, len(regions)+1)

	if defaultRegion != "" {
		if r, ok := findRegion(regions, defaultRegion); ok {
			out = append(out, r)
		} else {
			out = append(out, Region{Code: defaultRegion, Name: "(profile default)"})
		}
	}

	for _, r := range regions {
		if r.Code == defaultRegion {
			continue // 이미 맨 앞에 넣었다
		}

		out = append(out, r)
	}

	return out
}

func findRegion(regions []Region, code string) (Region, bool) {
	for _, r := range regions {
		if r.Code == code {
			return r, true
		}
	}

	return Region{}, false
}
