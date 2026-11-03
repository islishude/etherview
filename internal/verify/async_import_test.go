package verify

import (
	"context"
	"errors"
	"testing"
)

type importFailureSource struct{ err error }

func (s importFailureSource) ImportV2(context.Context, uint64, VerificationTarget) (SubmissionV2, error) {
	return SubmissionV2{}, s.err
}

type importFailureRepository struct{ verifyMemoryRepository }

func (*importFailureRepository) SaveImportInput(context.Context, VerificationLease, SubmissionV2) (VerificationJob, error) {
	return VerificationJob{}, errors.New("unexpected source persistence")
}
func (*importFailureRepository) AwaitImportCompiler(context.Context, VerificationLease) error {
	return errors.New("unexpected compilation handoff")
}

func TestSourcifyImportDurableErrorsAndBoundedRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  error
		attempt int
		code    ErrorCode
		retry   bool
	}{
		{"not found", ErrSourcifyNotFound, 1, ErrorSourcifyNotFound, false},
		{"mismatch", ErrSourcifyTargetMismatch, 1, ErrorSourcifyMismatch, false},
		{"invalid", ErrSourcifyInvalidResponse, 1, ErrorSourcifyInvalid, false},
		{"transient", errors.New("https://private.invalid/token"), 1, "", true},
		{"exhausted", errors.New("https://private.invalid/token"), 3, ErrorSourcifyUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &importFailureRepository{}
			repository.claimFound = true
			repository.lease = VerificationLease{Job: VerificationJob{ID: "import", Kind: JobSourcifyImport, AttemptCount: tc.attempt, MaxAttempts: 3, RequestV2: &SubmissionV2{Kind: JobSourcifyImport, SourceChainID: 1, Target: &VerificationTarget{}}}}
			worker, err := NewWorker(repository, &verifyTestCompiler{}, WorkerOptions{WorkerID: "import-errors", Importer: importFailureSource{tc.source}})
			if err != nil {
				t.Fatal(err)
			}
			found, err := worker.ProcessOne(t.Context())
			if !found || (tc.retry && !errors.Is(err, errImportRetry)) || (!tc.retry && err != nil) {
				t.Fatalf("process: %v %v", found, err)
			}
			if tc.retry {
				if len(repository.failures) != 0 {
					t.Fatal("temporary failure published terminal result")
				}
			} else if len(repository.failures) != 1 || repository.failures[0] != tc.code {
				t.Fatalf("errors: %v", repository.failures)
			}
		})
	}
}
