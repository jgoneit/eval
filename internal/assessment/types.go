// Package assessment grades supplied, normalized evidence. It never runs an
// agent or a checker, and provenance labels do not authenticate their producer.
package assessment

const (
	SuiteSchema          = "eval-suite/v1"
	LegacyAttemptsSchema = "eval-attempts/v1"
	AttemptsSchema       = "eval-attempts/v2"
	InputsSchema         = "eval-assessment-inputs/v1"
	AssessmentSchema     = "eval-assessment/v2"
	ComparisonSchema     = "eval-comparison/v2"
	EvaluatorVersion     = "rules-v2"
	MaxBytes             = 64 << 20
)

type Status string

const (
	Pass        Status = "pass"
	Fail        Status = "fail"
	Unavailable Status = "unavailable"
	Error       Status = "error"
)

type File struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}
type CheckCriterion struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	CheckerDigest string `json:"checker_digest"`
}
type Case struct {
	ID             string           `json:"id"`
	InputDigest    string           `json:"input_digest"`
	CriteriaDigest string           `json:"criteria_digest"`
	InitialFiles   []File           `json:"initial_files"`
	AllowedFiles   []string         `json:"allowed_files"`
	ProtectedFiles []string         `json:"protected_files"`
	RequiredChecks []CheckCriterion `json:"required_checks"`
}
type Suite struct {
	Schema  string `json:"schema"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}
type Condition struct {
	ID                string `json:"id"`
	InstructionDigest string `json:"instruction_digest"`
}
type Environment struct {
	Model             string `json:"model"`
	Reasoning         string `json:"reasoning"`
	PermissionProfile string `json:"permission_profile"`
	EnvironmentDigest string `json:"environment_digest"`
	ToolDigest        string `json:"tool_digest"`
}
type Coverage struct {
	Manifest    string `json:"manifest"`
	Tools       string `json:"tools"`
	Permissions string `json:"permissions"`
}
type Check struct {
	ID                  string `json:"id"`
	EvidenceID          string `json:"evidence_id"`
	Provenance          string `json:"provenance"`
	Executor            string `json:"executor"`
	CheckerDigest       string `json:"checker_digest"`
	ArtifactDigest      string `json:"artifact_digest"`
	ArtifactAfterDigest string `json:"artifact_after_digest"`
	Status              Status `json:"status"`
}

// Event omits commands, paths, messages, reasoning, and tool output by design.
type Event struct {
	ID          string `json:"id"`
	Sequence    int    `json:"sequence"`
	Kind        string `json:"kind"`
	Provenance  string `json:"provenance"`
	Tool        string `json:"tool,omitempty"`
	Status      string `json:"status"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type Measurements struct {
	Provenance        string `json:"provenance"`
	DurationMS        *int64 `json:"duration_ms"`
	InputTokens       *int64 `json:"input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
	CachedInputTokens *int64 `json:"cached_input_tokens"`
}

// ConfigurationObservation records controller observations, not which settings
// an Agent actually loaded. Missing legacy evidence remains unavailable.
type ConfigurationObservation struct {
	Status         string  `json:"status"`
	ExpectedDigest *string `json:"expected_digest"`
	BeforeDigest   *string `json:"before_digest"`
	AfterDigest    *string `json:"after_digest"`
}
type Attempt struct {
	ConfigurationObservation *ConfigurationObservation `json:"configuration_observation,omitempty"`
	ID                       string                    `json:"id"`
	CaseID                   string                    `json:"case_id"`
	Termination              string                    `json:"termination"`
	ArtifactDigest           string                    `json:"artifact_digest"`
	Files                    []File                    `json:"files"`
	ManifestProvenance       string                    `json:"manifest_provenance"`
	ManifestEvidenceID       string                    `json:"manifest_evidence_id"`
	Coverage                 Coverage                  `json:"coverage"`
	Checks                   []Check                   `json:"checks"`
	Events                   []Event                   `json:"events"`
	Measurements             Measurements              `json:"measurements"`
}
type AttemptSet struct {
	Schema       string      `json:"schema"`
	SuiteID      string      `json:"suite_id"`
	SuiteVersion string      `json:"suite_version"`
	SuiteDigest  string      `json:"suite_digest"`
	Condition    Condition   `json:"condition"`
	Environment  Environment `json:"environment"`
	Attempts     []Attempt   `json:"attempts"`
}

// AssessmentInputs are private: manifests can contain repository-relative paths.
// They must never be emitted as an aggregate assessment or comparison.
type AssessmentInputs struct {
	Schema   string     `json:"schema"`
	Suite    Suite      `json:"suite"`
	Attempts AttemptSet `json:"attempts"`
}
type AssessmentRecord struct {
	Assessment Assessment
	Inputs     AssessmentInputs
}
type Evidence struct {
	CriterionID string `json:"criterion_id,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Sequence    int    `json:"sequence,omitempty"`
	Status      string `json:"status,omitempty"`
	Executor    string `json:"executor,omitempty"`
	ID          string `json:"id"`
	Provenance  string `json:"provenance"`
	Kind        string `json:"kind"`
}
type Finding struct {
	Kind        string   `json:"kind,omitempty"`
	ID          string   `json:"id"`
	Status      Status   `json:"status"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Counts struct {
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Unavailable int `json:"unavailable"`
	Error       int `json:"error"`
}
type ToolCount struct {
	Tool          string `json:"tool"`
	Calls         int    `json:"calls"`
	Failures      int    `json:"failures"`
	RepeatedCalls int    `json:"repeated_calls"`
}
type VerificationCounts struct {
	Agent       int `json:"agent"`
	Independent int `json:"independent"`
}
type CaseAssessment struct {
	ConfigurationObservation ConfigurationObservation `json:"configuration_observation"`
	CaseID                   string                   `json:"case_id"`
	InputDigest              string                   `json:"input_digest"`
	CriteriaDigest           string                   `json:"criteria_digest"`
	AttemptID                string                   `json:"attempt_id,omitempty"`
	Termination              string                   `json:"termination"`
	ArtifactDigest           string                   `json:"artifact_digest,omitempty"`
	Outcome                  Status                   `json:"outcome"`
	Requirements             Status                   `json:"requirements"`
	Regressions              Status                   `json:"regressions"`
	Checks                   []Finding                `json:"checks"`
	Evidence                 []Evidence               `json:"evidence"`
	Process                  Status                   `json:"process"`
	Rules                    []Finding                `json:"rules"`
	Coverage                 Coverage                 `json:"coverage"`
	Tools                    []ToolCount              `json:"tools"`
	Verification             VerificationCounts       `json:"verification"`
	Measurements             Measurements             `json:"measurements"`
}
type Summary struct {
	Planned   int    `json:"planned"`
	Attempted int    `json:"attempted"`
	Missing   int    `json:"missing"`
	Outcome   Counts `json:"outcome"`
	Process   Counts `json:"process"`
}
type Assessment struct {
	InputsDigest     string           `json:"inputs_digest"`
	Schema           string           `json:"schema"`
	EvaluatorVersion string           `json:"evaluator_version"`
	SuiteID          string           `json:"suite_id"`
	SuiteVersion     string           `json:"suite_version"`
	SuiteDigest      string           `json:"suite_digest"`
	Condition        Condition        `json:"condition"`
	Environment      Environment      `json:"environment"`
	ProvenancePolicy string           `json:"provenance_policy"`
	Cases            []CaseAssessment `json:"cases"`
	Summary          Summary          `json:"summary"`
}
type MeasurementDelta struct {
	DurationMS        *int64 `json:"duration_ms"`
	InputTokens       *int64 `json:"input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
	CachedInputTokens *int64 `json:"cached_input_tokens"`
}
type RuleComparison struct {
	ID        string `json:"id"`
	Baseline  Status `json:"baseline"`
	Candidate Status `json:"candidate"`
	Change    string `json:"change"`
}
type CaseComparison struct {
	BaselineConfiguration   string           `json:"baseline_configuration"`
	CandidateConfiguration  string           `json:"candidate_configuration"`
	ConfigurationComparable bool             `json:"configuration_comparable"`
	BaselineProcess         Status           `json:"baseline_process"`
	CandidateProcess        Status           `json:"candidate_process"`
	Rules                   []RuleComparison `json:"rules"`
	CaseID                  string           `json:"case_id"`
	Baseline                Status           `json:"baseline"`
	Candidate               Status           `json:"candidate"`
	OutcomeChange           string           `json:"outcome_change"`
	ProcessChange           string           `json:"process_change"`
	BaselineTermination     string           `json:"baseline_termination"`
	CandidateTermination    string           `json:"candidate_termination"`
	Delta                   MeasurementDelta `json:"delta"`
}
type ComparisonSummary struct {
	Planned                        int `json:"planned"`
	Comparable                     int `json:"comparable"`
	Improved                       int `json:"improved"`
	Regressed                      int `json:"regressed"`
	Unchanged                      int `json:"unchanged"`
	Unevaluated                    int `json:"unevaluated"`
	BaselineMissing                int `json:"baseline_missing"`
	CandidateMissing               int `json:"candidate_missing"`
	MeasuredPairs                  int `json:"measured_pairs"`
	DurationMeasuredPairs          int `json:"duration_measured_pairs"`
	InputTokensMeasuredPairs       int `json:"input_tokens_measured_pairs"`
	OutputTokensMeasuredPairs      int `json:"output_tokens_measured_pairs"`
	CachedInputTokensMeasuredPairs int `json:"cached_input_tokens_measured_pairs"`
}
type Comparison struct {
	Kind             string            `json:"kind"`
	Schema           string            `json:"schema"`
	EvaluatorVersion string            `json:"evaluator_version"`
	SuiteID          string            `json:"suite_id"`
	SuiteVersion     string            `json:"suite_version"`
	SuiteDigest      string            `json:"suite_digest"`
	Baseline         Condition         `json:"baseline"`
	Candidate        Condition         `json:"candidate"`
	Environment      Environment       `json:"environment"`
	Cases            []CaseComparison  `json:"cases"`
	Summary          ComparisonSummary `json:"summary"`
}
