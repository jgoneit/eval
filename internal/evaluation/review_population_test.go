package evaluation

import "testing"

func TestPopulationExclusionRequiresPredeterminedReason(t *testing.T) {
	s := reviewFixture()
	r, err := ReviewTemplate(s)
	if err != nil {
		t.Fatal(err)
	}
	r.Tasks[0].Eligibility = "no"
	for _, reason := range []string{"", "tool_unused", "failed_observation", "bad_outcome"} {
		r.Tasks[0].ExclusionReason = reason
		if ValidateReview(s, r) == nil {
			t.Fatalf("accepted posthoc or missing reason %q", reason)
		}
	}
	r.Tasks[0].ExclusionReason = "pure_qa"
	if err = ValidateReview(s, r); err != nil {
		t.Fatal(err)
	}
}
