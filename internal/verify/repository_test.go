package verify

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var verifyDriverSequence atomic.Uint64

type verifySQLBackend struct {
	mu    sync.Mutex
	query func(string, []driver.NamedValue) (driver.Rows, error)
	exec  func(string, []driver.NamedValue) (driver.Result, error)
}

type verifySQLDriver struct{ backend *verifySQLBackend }
type verifySQLConn struct{ backend *verifySQLBackend }
type verifySQLTx struct{}

func (database verifySQLDriver) Open(string) (driver.Conn, error) {
	return &verifySQLConn{backend: database.backend}, nil
}

func (connection *verifySQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported by the verification SQL fake")
}

func (connection *verifySQLConn) Close() error { return nil }

func (connection *verifySQLConn) Begin() (driver.Tx, error) { return verifySQLTx{}, nil }

func (connection *verifySQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return verifySQLTx{}, nil
}

func (connection *verifySQLConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	connection.backend.mu.Lock()
	defer connection.backend.mu.Unlock()
	if connection.backend.query == nil {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	return connection.backend.query(query, arguments)
}

func (connection *verifySQLConn) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	connection.backend.mu.Lock()
	defer connection.backend.mu.Unlock()
	if connection.backend.exec == nil {
		return nil, fmt.Errorf("unexpected exec: %s", query)
	}
	return connection.backend.exec(query, arguments)
}

func (verifySQLTx) Commit() error   { return nil }
func (verifySQLTx) Rollback() error { return nil }

type verifySQLRows struct {
	columns []string
	values  [][]driver.Value
	next    int
}

func (rows *verifySQLRows) Columns() []string { return rows.columns }
func (rows *verifySQLRows) Close() error      { return nil }

func (rows *verifySQLRows) Next(destination []driver.Value) error {
	if rows.next >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.next])
	rows.next++
	return nil
}

func openVerifyDB(t *testing.T, backend *verifySQLBackend) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("etherview_verify_fake_%d", verifyDriverSequence.Add(1))
	sql.Register(name, verifySQLDriver{backend: backend})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(64)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func verifyRows(values ...[]driver.Value) driver.Rows {
	columns := make([]string, 21)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return &verifySQLRows{columns: columns, values: values}
}

func oneColumnVerifyRows(value driver.Value) driver.Rows {
	return &verifySQLRows{columns: []string{"value"}, values: [][]driver.Value{{value}}}
}

func verifyJobRow(id string, request Request, status JobStatus, resultKind, result, errorCode driver.Value) []driver.Value {
	requestJSON, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	address, _ := hex.DecodeString(request.Address[2:])
	codeHash, _ := hex.DecodeString(request.CodeHash[2:])
	blockHash, _ := hex.DecodeString(request.AtBlockHash[2:])
	now := time.Unix(1_700_000_000, 0).UTC()
	requestDigest := verificationRequestDigest(requestJSON, false)
	attemptCount := 0
	var compilerKind, compilerDigest, compilerHard driver.Value
	if status != JobQueued {
		attemptCount = 1
	}
	if status == JobRunning || status == JobSucceeded {
		digest := make([]byte, 32)
		digest[0] = 1
		compilerKind, compilerDigest, compilerHard = string(CompilerProcess), digest, false
	}
	return []driver.Value{
		id,
		fmt.Sprintf("%d", request.ChainID),
		address,
		codeHash,
		blockHash,
		string(request.Language),
		request.CompilerVersion,
		requestJSON,
		string(status),
		resultKind,
		result,
		errorCode,
		now,
		now,
		requestDigest[:],
		false,
		attemptCount,
		3,
		compilerKind,
		compilerDigest,
		compilerHard,
	}
}

func validVerifyRequest() Request {
	runtimeBytecode := []byte{0x60, 0x01}
	return Request{
		ChainID:            1,
		Address:            "0x" + strings.Repeat("11", 20),
		CodeHash:           "0x" + hex.EncodeToString(keccak256Bytes(runtimeBytecode)),
		AtBlockHash:        "0x" + strings.Repeat("33", 32),
		Language:           LanguageSolidity,
		CompilerVersion:    "0.8.30",
		ContractIdentifier: "A.sol:A",
		StandardJSON:       json.RawMessage(`{"language":"Solidity","sources":{"A.sol":{"content":"contract A {}"}},"settings":{}}`),
		CreationBytecode:   "0x6001",
		RuntimeBytecode:    "0x" + hex.EncodeToString(runtimeBytecode),
	}
}

func verificationID(sequence int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", sequence)
}

func newVerifyRepository(t *testing.T, backend *verifySQLBackend) *PostgresRepository {
	t.Helper()
	repository, err := NewPostgresRepository(openVerifyDB(t, backend), RepositoryOptions{MaxRequestBytes: 64 << 10, MaxResultBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestPostgresRepositorySubmitIsIdempotentForBoundIdentity(t *testing.T) {
	request := validVerifyRequest()
	var stored []driver.Value
	insertions := 0
	backend := &verifySQLBackend{}
	backend.query = func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "INSERT INTO verification_jobs"):
			insertions++
			if !strings.Contains(query, "ON CONFLICT (chain_id, address, code_hash, block_hash, request_digest)") ||
				!strings.Contains(query, "status IN ('queued', 'running', 'succeeded')") {
				return nil, errors.New("submission does not use the bound identity")
			}
			if insertions == 1 {
				stored = verifyJobRow(arguments[0].Value.(string), request, JobQueued, nil, nil, nil)
				return verifyRows(stored), nil
			}
			return verifyRows(), nil
		case strings.Contains(query, "FROM verification_jobs") && strings.Contains(query, "chain_id = $1"):
			return verifyRows(stored), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}
	repository := newVerifyRepository(t, backend)

	first, created, err := repository.Submit(context.Background(), request)
	if err != nil || !created {
		t.Fatalf("first submit created=%v err=%v", created, err)
	}
	second, created, err := repository.Submit(context.Background(), request)
	if err != nil || created {
		t.Fatalf("second submit created=%v err=%v", created, err)
	}
	if first.ID != second.ID || first.Request.CodeHash != request.CodeHash {
		t.Fatalf("idempotent jobs differ: first=%#v second=%#v", first, second)
	}
}

func TestPostgresRepositorySubmitRetriesWhenConflictingJobBecomesFailed(t *testing.T) {
	request := validVerifyRequest()
	inserts, selects := 0, 0
	backend := &verifySQLBackend{}
	backend.query = func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "INSERT INTO verification_jobs"):
			inserts++
			if inserts == 1 {
				return verifyRows(), nil
			}
			return verifyRows(verifyJobRow(arguments[0].Value.(string), request, JobQueued, nil, nil, nil)), nil
		case strings.Contains(query, "FROM verification_jobs") && strings.Contains(query, "request_digest = $5"):
			selects++
			return verifyRows(), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}
	repository := newVerifyRepository(t, backend)
	job, created, err := repository.Submit(context.Background(), request)
	if err != nil || !created || job.ID == "" || inserts != 2 || selects != 1 {
		t.Fatalf("job=%+v created=%t inserts=%d selects=%d error=%v", job, created, inserts, selects, err)
	}
}

func TestPostgresRepositoryClaimReclaimsExpiredLeaseWithNewToken(t *testing.T) {
	request := validVerifyRequest()
	var tokens []string
	backend := &verifySQLBackend{}
	backend.query = func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "FOR UPDATE SKIP LOCKED") ||
			!strings.Contains(query, "status = 'running' AND lease_expires_at <= clock_timestamp()") ||
			!strings.Contains(query, "attempt_count >= max_attempts") ||
			!strings.Contains(query, "attempt_count = job.attempt_count + 1") ||
			!strings.Contains(query, "SET status = 'running'") {
			return nil, errors.New("claim does not atomically recover queued and expired jobs")
		}
		tokens = append(tokens, arguments[1].Value.(string))
		return verifyRows(verifyJobRow(verificationID(1), request, JobRunning, nil, nil, nil)), nil
	}
	repository := newVerifyRepository(t, backend)

	first, found, err := repository.Claim(context.Background(), "worker-a", time.Minute)
	if err != nil || !found {
		t.Fatalf("first claim found=%v err=%v", found, err)
	}
	second, found, err := repository.Claim(context.Background(), "worker-b", time.Minute)
	if err != nil || !found {
		t.Fatalf("second claim found=%v err=%v", found, err)
	}
	if first.Job.ID != second.Job.ID || first.Token == second.Token || len(tokens) != 2 {
		t.Fatalf("unexpected reclaimed leases: first=%#v second=%#v tokens=%v", first, second, tokens)
	}
}

func TestPostgresRepositoryParallelClaimsHaveUniqueJobsAndTokens(t *testing.T) {
	const claims = 24
	request := validVerifyRequest()
	next := 0
	backend := &verifySQLBackend{}
	backend.query = func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "FOR UPDATE SKIP LOCKED") {
			return nil, errors.New("missing skip-locked claim")
		}
		next++
		return verifyRows(verifyJobRow(verificationID(next), request, JobRunning, nil, nil, nil)), nil
	}
	repository := newVerifyRepository(t, backend)

	type result struct {
		lease VerificationLease
		err   error
	}
	results := make(chan result, claims)
	var group sync.WaitGroup
	for range claims {
		group.Add(1)
		go func() {
			defer group.Done()
			lease, found, err := repository.Claim(context.Background(), "worker", time.Minute)
			if err == nil && !found {
				err = errors.New("claim unexpectedly empty")
			}
			results <- result{lease: lease, err: err}
		}()
	}
	group.Wait()
	close(results)
	jobs, tokens := make(map[string]struct{}), make(map[string]struct{})
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		jobs[result.lease.Job.ID] = struct{}{}
		tokens[result.lease.Token] = struct{}{}
	}
	if len(jobs) != claims || len(tokens) != claims {
		t.Fatalf("jobs=%d tokens=%d, want %d unique values", len(jobs), len(tokens), claims)
	}
}

func TestPostgresRepositoryLeaseGuardAndMismatchPrivacy(t *testing.T) {
	request := validVerifyRequest()
	lease := VerificationLease{Job: VerificationJob{ID: verificationID(1), Request: request}, Token: "lease-token"}
	t.Run("lease lost", func(t *testing.T) {
		var statements []string
		backend := &verifySQLBackend{
			query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
				statements = append(statements, query)
				return verifyRows(), nil
			},
			exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
				statements = append(statements, query)
				return driver.RowsAffected(0), nil
			},
		}
		repository := newVerifyRepository(t, backend)
		if err := repository.Renew(context.Background(), lease, time.Minute); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("renew error=%v", err)
		}
		if err := repository.Fail(context.Background(), lease, ErrorCompileFailed); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("fail error=%v", err)
		}
		completion := Completion{Kind: MatchMismatch, Match: MatchResult{Creation: MatchMismatch, Runtime: MatchExact}}
		if err := repository.Complete(context.Background(), lease, completion); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("complete error=%v", err)
		}
		if len(statements) != 3 {
			t.Fatalf("statements=%d, want 3", len(statements))
		}
		for _, statement := range statements {
			if !strings.Contains(statement, "lease_token = $2") || !strings.Contains(statement, "lease_expires_at > clock_timestamp()") {
				t.Fatalf("lease guard missing from %q", statement)
			}
		}
	})

	t.Run("mismatch has no publishable material", func(t *testing.T) {
		queries := 0
		var statements []string
		backend := &verifySQLBackend{
			query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
				queries++
				if strings.Contains(query, "FROM verification_jobs") {
					return verifyRows(verifyJobRow(verificationID(1), request, JobRunning, nil, nil, nil)), nil
				}
				if strings.Contains(query, "FROM contract_code_observations") {
					return oneColumnVerifyRows("42"), nil
				}
				return nil, fmt.Errorf("unexpected query: %s", query)
			},
			exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
				statements = append(statements, query)
				if strings.Contains(query, "verified_contracts") {
					return nil, errors.New("mismatch attempted to publish contract material")
				}
				if strings.Contains(query, "verification_results") {
					encoded, ok := arguments[11].Value.(string)
					if !ok || strings.Contains(encoded, "abi") || strings.Contains(encoded, "sources") ||
						arguments[12].Value != nil || arguments[13].Value != nil ||
						arguments[14].Value != nil || arguments[15].Value != nil {
						return nil, errors.New("mismatch result contains publishable material")
					}
				}
				return driver.RowsAffected(1), nil
			},
		}
		repository := newVerifyRepository(t, backend)
		completion := Completion{Kind: MatchMismatch, Match: MatchResult{Creation: MatchMismatch, Runtime: MatchExact}}
		if err := repository.Complete(context.Background(), lease, completion); err != nil {
			t.Fatal(err)
		}
		if queries != 2 || len(statements) != 2 ||
			!strings.Contains(statements[0], "INSERT INTO verification_results") ||
			!strings.Contains(statements[1], "SET status = 'succeeded'") {
			t.Fatalf("queries=%d statements=%q", queries, statements)
		}
	})
}

func TestPostgresRepositoryPublishesOnlyVerifiedCompletion(t *testing.T) {
	request := validVerifyRequest()
	lease := VerificationLease{Job: VerificationJob{ID: verificationID(1), Request: request}, Token: "lease-token"}
	var statements []string
	backend := &verifySQLBackend{
		query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
			if strings.Contains(query, "FROM verification_jobs") {
				return verifyRows(verifyJobRow(verificationID(1), request, JobRunning, nil, nil, nil)), nil
			}
			if !strings.Contains(query, "FROM contract_code_observations AS observation") ||
				!strings.Contains(query, "JOIN canonical_blocks AS canonical") ||
				!strings.Contains(query, "observation.address = $2") ||
				!strings.Contains(query, "observation.code_hash = $3") ||
				!strings.Contains(query, "observation.block_hash = $4") ||
				!strings.Contains(query, "observation.canonical = TRUE") ||
				!strings.Contains(query, "FOR SHARE OF observation, canonical") {
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
			address, _ := hex.DecodeString(request.Address[2:])
			codeHash, _ := hex.DecodeString(request.CodeHash[2:])
			blockHash, _ := hex.DecodeString(request.AtBlockHash[2:])
			if len(arguments) != 4 || arguments[0].Value != "1" ||
				!bytes.Equal(arguments[1].Value.([]byte), address) ||
				!bytes.Equal(arguments[2].Value.([]byte), codeHash) ||
				!bytes.Equal(arguments[3].Value.([]byte), blockHash) {
				return nil, fmt.Errorf("canonical target arguments = %#v", arguments)
			}
			return oneColumnVerifyRows("42"), nil
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			statements = append(statements, query)
			return driver.RowsAffected(1), nil
		},
	}
	repository := newVerifyRepository(t, backend)
	completion := Completion{
		Kind:     MatchExact,
		Match:    MatchResult{Creation: MatchExact, Runtime: MatchExact},
		Artifact: Artifact{ABI: json.RawMessage(`[]`)},
		Sources:  json.RawMessage(`{"A.sol":{"content":"contract A {}"}}`),
		Settings: json.RawMessage(`{}`),
	}
	if err := repository.Complete(context.Background(), lease, completion); err != nil {
		t.Fatal(err)
	}
	if len(statements) != 3 || !strings.Contains(statements[0], "INSERT INTO verification_results") ||
		!strings.Contains(statements[1], "INSERT INTO verified_contracts") ||
		!strings.Contains(statements[2], "SET status = 'succeeded'") {
		t.Fatalf("unexpected completion statements: %v", statements)
	}
}

func TestPostgresRepositoryDoesNotPublishWithoutCanonicalCodeObservation(t *testing.T) {
	request := validVerifyRequest()
	lease := VerificationLease{Job: VerificationJob{ID: verificationID(1), Request: request}, Token: "lease-token"}
	executions := 0
	backend := &verifySQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			if strings.Contains(query, "FROM verification_jobs") {
				return verifyRows(verifyJobRow(verificationID(1), request, JobRunning, nil, nil, nil)), nil
			}
			if !strings.Contains(query, "FROM contract_code_observations AS observation") || !strings.Contains(query, "JOIN canonical_blocks AS canonical") {
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
			return &verifySQLRows{columns: []string{"block_number"}}, nil
		},
		exec: func(string, []driver.NamedValue) (driver.Result, error) {
			executions++
			return driver.RowsAffected(1), nil
		},
	}
	repository := newVerifyRepository(t, backend)
	completion := Completion{
		Kind: MatchExact, Match: MatchResult{Creation: MatchExact, Runtime: MatchExact},
		Artifact: Artifact{ABI: json.RawMessage(`[]`)},
		Sources:  json.RawMessage(`{"A.sol":{"content":"contract A {}"}}`),
		Settings: json.RawMessage(`{}`),
	}
	err := repository.Complete(context.Background(), lease, completion)
	if !errors.Is(err, ErrTargetNotCanonical) || executions != 1 {
		t.Fatalf("error=%v executions=%d", err, executions)
	}
}

func TestPostgresRepositoryQueryBoundariesValidatePersistedData(t *testing.T) {
	request := validVerifyRequest()
	result := `{"match":{"creation":"exact","runtime":"exact"},"published":true}`
	backend := &verifySQLBackend{}
	backend.query = func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "WHERE id = $1::uuid"):
			return verifyRows(verifyJobRow(verificationID(7), request, JobSucceeded, "exact", []byte(result), nil)), nil
		case strings.Contains(query, "FROM verified_contracts"):
			address, _ := hex.DecodeString(request.Address[2:])
			codeHash, _ := hex.DecodeString(request.CodeHash[2:])
			return &verifySQLRows{
				columns: []string{"chain_id", "address", "code_hash", "from", "to", "language", "version", "kind", "name", "abi", "sources", "settings", "created"},
				values:  [][]driver.Value{{"1", address, codeHash, "42", nil, "solidity", "0.8.30", "exact", "A", []byte(`[]`), []byte(`{"A.sol":{}}`), []byte(`{}`), time.Unix(1_700_000_000, 0).UTC()}},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}
	repository := newVerifyRepository(t, backend)
	job, found, err := repository.Job(context.Background(), verificationID(7))
	if err != nil || !found || job.Result == nil || !job.Result.Published {
		t.Fatalf("job=%#v found=%v err=%v", job, found, err)
	}
	contract, found, err := repository.VerifiedContract(context.Background(), 1, request.Address, request.CodeHash)
	if err != nil || !found || contract.ValidFromBlock != 42 || contract.ContractName != "A" {
		t.Fatalf("contract=%#v found=%v err=%v", contract, found, err)
	}
}
