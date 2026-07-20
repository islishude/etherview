package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const DefaultPartitionSpan uint64 = 1_000_000

const partitionAdvisoryLock = "etherview:block-partitions"

type blockPartitionSpec struct {
	Parent       string
	Default      string
	NameCode     string
	Dependencies []string
}

type pendingPartition struct {
	spec blockPartitionSpec
	name string
}

// Attach order is parent before child so PostgreSQL can validate every foreign
// key while a pre-existing DEFAULT range is moved into the new partitions.
var blockPartitionSpecs = []blockPartitionSpec{
	{Parent: "transaction_inclusions", Default: "transaction_inclusions_default", NameCode: "txi"},
	{Parent: "receipts", Default: "receipts_default", NameCode: "rcp", Dependencies: []string{"transaction_inclusions"}},
	{Parent: "logs", Default: "logs_default", NameCode: "log", Dependencies: []string{"receipts"}},
	{Parent: "withdrawals", Default: "withdrawals_default", NameCode: "wdr"},
	{Parent: "abi_decodings", Default: "abi_decodings_default", NameCode: "abi"},
	{Parent: "token_events", Default: "token_events_default", NameCode: "tev", Dependencies: []string{"logs"}},
	{Parent: "token_balance_deltas", Default: "token_balance_deltas_default", NameCode: "tbd", Dependencies: []string{"token_events"}},
	{Parent: "normalized_traces", Default: "normalized_traces_default", NameCode: "trc", Dependencies: []string{"transaction_inclusions"}},
	{Parent: "address_activities", Default: "address_activities_default", NameCode: "act"},
}

// Rows must leave child tables before their referenced parents. This ordering
// is the reverse dependency order of blockPartitionSpecs.
var blockPartitionDeleteOrder = []string{
	"token_balance_deltas",
	"token_events",
	"normalized_traces",
	"abi_decodings",
	"logs",
	"receipts",
	"address_activities",
	"withdrawals",
	"transaction_inclusions",
}

type partitionRangeCache struct {
	mu      sync.RWMutex
	ensured map[uint64]struct{}
}

func newPartitionRangeCache() partitionRangeCache {
	return partitionRangeCache{ensured: make(map[uint64]struct{})}
}

func (c *partitionRangeCache) contains(lower uint64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, exists := c.ensured[lower]
	return exists
}

func (c *partitionRangeCache) add(ranges ...uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, lower := range ranges {
		c.ensured[lower] = struct{}{}
	}
}

// PartitionRecoveryError identifies schema/data state that the automatic
// partition mover cannot safely reinterpret. Retrying after correcting the
// named relation is safe because all automatic DDL and row moves are atomic.
type PartitionRecoveryError struct {
	Table string
	Lower uint64
	Upper uint64
	Cause string
}

func (e *PartitionRecoveryError) Error() string {
	return fmt.Sprintf(
		"partition recovery required for %s range [%d,%d): %s; stop writers, preserve rows in dependency order, correct the named partition, then retry",
		e.Table, e.Lower, e.Upper, e.Cause,
	)
}

// EnsureBlockPartitions retains the database-level provisioning API for
// migration and operator callers. Runtime repository users should call the
// method on PostgresRepository so successfully provisioned ranges are cached.
func EnsureBlockPartitions(ctx context.Context, db *sql.DB, start, end, span uint64) error {
	if db == nil {
		return errors.New("ensure partitions: nil database")
	}
	span, err := validatePartitionRequest(start, end, span)
	if err != nil {
		return err
	}
	for _, lower := range partitionRangeStarts(start, end, span) {
		if err := ensurePartitionRange(ctx, db, lower, lower+span); err != nil {
			return err
		}
	}
	return nil
}

// EnsureBlockPartitions pre-creates every fixed-width range intersecting the
// requested half-open interval and remembers committed ranges for hot-path
// bundle writes.
func (r *PostgresRepository) EnsureBlockPartitions(ctx context.Context, start, end uint64) error {
	if r == nil || r.db == nil {
		return errors.New("ensure partitions: nil repository")
	}
	span, err := validatePartitionRequest(start, end, DefaultPartitionSpan)
	if err != nil {
		return err
	}
	for _, lower := range partitionRangeStarts(start, end, span) {
		if r.partitions.contains(lower) {
			continue
		}
		if err := ensurePartitionRange(ctx, r.db, lower, lower+span); err != nil {
			return err
		}
		r.partitions.add(lower)
	}
	return nil
}

func validatePartitionRequest(start, end, span uint64) (uint64, error) {
	if span == 0 {
		span = DefaultPartitionSpan
	}
	if span != DefaultPartitionSpan {
		return 0, fmt.Errorf("ensure partitions: span must be %d", DefaultPartitionSpan)
	}
	if end <= start {
		return 0, errors.New("ensure partitions: end must exceed start")
	}
	last := end - 1
	lower := last - last%span
	if lower > math.MaxUint64-span {
		return 0, errors.New("ensure partitions: uint64 range overflow")
	}
	return span, nil
}

func partitionRangeStarts(start, end, span uint64) []uint64 {
	first := start - start%span
	last := (end - 1) - (end-1)%span
	ranges := make([]uint64, 0, (last-first)/span+1)
	for lower := first; ; lower += span {
		ranges = append(ranges, lower)
		if lower == last {
			return ranges
		}
	}
}

func ensurePartitionRange(ctx context.Context, db *sql.DB, lower, upper uint64) error {
	// READ COMMITTED is intentional: a competing process may have waited on
	// the global advisory lock after its transaction began. The catalog recheck
	// must use a fresh statement snapshot so committed DDL is visible.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin partition provisioning: %w", err)
	}
	defer tx.Rollback()
	if err := ensurePartitionRangeTx(ctx, tx, lower, upper); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit partition provisioning: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ensureBundlePartitionsTx(
	ctx context.Context,
	tx *sql.Tx,
	references []BlockRef,
) ([]uint64, error) {
	unique := make(map[uint64]struct{}, len(references))
	for _, reference := range references {
		lower := reference.Number - reference.Number%DefaultPartitionSpan
		if r.partitions.contains(lower) {
			continue
		}
		if lower > math.MaxUint64-DefaultPartitionSpan {
			return nil, errors.New("ensure bundle partitions: uint64 range overflow")
		}
		unique[lower] = struct{}{}
	}
	ranges := make([]uint64, 0, len(unique))
	for lower := range unique {
		ranges = append(ranges, lower)
	}
	sort.Slice(ranges, func(left, right int) bool { return ranges[left] < ranges[right] })
	for _, lower := range ranges {
		if err := ensurePartitionRangeTx(ctx, tx, lower, lower+DefaultPartitionSpan); err != nil {
			return nil, err
		}
	}
	return ranges, nil
}

func (r *PostgresRepository) bundleWriteIsolation(references []BlockRef) sql.IsolationLevel {
	for _, reference := range references {
		lower := reference.Number - reference.Number%DefaultPartitionSpan
		if !r.partitions.contains(lower) {
			// A statement snapshot must be refreshed after waiting for a
			// competing process's partition DDL to commit.
			return sql.LevelReadCommitted
		}
	}
	return sql.LevelSerializable
}

func ensurePartitionRangeTx(ctx context.Context, tx *sql.Tx, lower, upper uint64) error {
	if lower%DefaultPartitionSpan != 0 || upper-lower != DefaultPartitionSpan {
		return errors.New("partition range is not aligned to the fixed span")
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1))`, partitionAdvisoryLock); err != nil {
		return fmt.Errorf("lock partition lifecycle: %w", err)
	}
	var schema string
	if err := tx.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return fmt.Errorf("resolve partition schema: %w", err)
	}
	if schema == "" {
		return errors.New("resolve partition schema: current schema is empty")
	}
	if err := lockPartitionTables(ctx, tx, schema); err != nil {
		return err
	}

	pending := make([]pendingPartition, 0, len(blockPartitionSpecs))
	existing := make(map[string]bool, len(blockPartitionSpecs))
	for _, spec := range blockPartitionSpecs {
		attached, err := partitionAttachedForRange(ctx, tx, schema, spec, lower, upper)
		if err != nil {
			return err
		}
		existing[spec.Parent] = attached
		if attached {
			continue
		}
		name := partitionName(spec, lower, upper)
		if relationExists, err := relationExistsTx(ctx, tx, schema, name); err != nil {
			return err
		} else if relationExists {
			return &PartitionRecoveryError{
				Table: spec.Parent, Lower: lower, Upper: upper,
				Cause: fmt.Sprintf("relation %s exists but is not the expected attached partition", name),
			}
		}
		pending = append(pending, pendingPartition{spec: spec, name: name})
	}
	if len(pending) == 0 {
		return nil
	}
	if err := validatePartialPartitionDependencies(ctx, tx, schema, pending, existing, lower, upper); err != nil {
		return err
	}

	for _, partition := range pending {
		parent := qualifiedIdentifier(schema, partition.spec.Parent)
		child := qualifiedIdentifier(schema, partition.name)
		defaultTable := qualifiedIdentifier(schema, partition.spec.Default)
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			"CREATE TABLE %s (LIKE %s INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING STORAGE)",
			child, parent,
		)); err != nil {
			return fmt.Errorf("create staging partition %s: %w", partition.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			"INSERT INTO %s SELECT * FROM %s WHERE block_number >= $1::numeric AND block_number < $2::numeric",
			child, defaultTable,
		), decimal(lower), decimal(upper)); err != nil {
			return fmt.Errorf("copy DEFAULT rows for %s range [%d,%d): %w", partition.spec.Parent, lower, upper, err)
		}
	}

	pendingByParent := make(map[string]pendingPartition, len(pending))
	for _, partition := range pending {
		pendingByParent[partition.spec.Parent] = partition
	}
	for _, parent := range blockPartitionDeleteOrder {
		partition, move := pendingByParent[parent]
		if !move {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			"DELETE FROM %s WHERE block_number >= $1::numeric AND block_number < $2::numeric",
			qualifiedIdentifier(schema, partition.spec.Default),
		), decimal(lower), decimal(upper)); err != nil {
			return &PartitionRecoveryError{
				Table: parent, Lower: lower, Upper: upper,
				Cause: fmt.Sprintf("cannot evacuate DEFAULT rows atomically: %v", err),
			}
		}
	}

	for _, spec := range blockPartitionSpecs {
		partition, attach := pendingByParent[spec.Parent]
		if !attach {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			"ALTER TABLE %s ATTACH PARTITION %s FOR VALUES FROM (%s) TO (%s)",
			qualifiedIdentifier(schema, spec.Parent),
			qualifiedIdentifier(schema, partition.name),
			strconv.FormatUint(lower, 10), strconv.FormatUint(upper, 10),
		)); err != nil {
			return &PartitionRecoveryError{
				Table: spec.Parent, Lower: lower, Upper: upper,
				Cause: fmt.Sprintf("attach failed after DEFAULT evacuation: %v", err),
			}
		}
	}
	return nil
}

func lockPartitionTables(ctx context.Context, tx *sql.Tx, schema string) error {
	tables := make([]string, 0, len(blockPartitionSpecs)*2)
	for _, spec := range blockPartitionSpecs {
		tables = append(tables,
			qualifiedIdentifier(schema, spec.Parent),
			qualifiedIdentifier(schema, spec.Default),
		)
	}
	if _, err := tx.ExecContext(ctx,
		"LOCK TABLE "+strings.Join(tables, ", ")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("lock partitioned fact tables: %w", err)
	}
	return nil
}

func partitionAttachedForRange(
	ctx context.Context,
	tx *sql.Tx,
	schema string,
	spec blockPartitionSpec,
	lower, upper uint64,
) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT child.relname, pg_get_expr(child.relpartbound, child.oid)
		FROM pg_inherits inheritance
		JOIN pg_class parent ON parent.oid = inheritance.inhparent
		JOIN pg_namespace parent_namespace ON parent_namespace.oid = parent.relnamespace
		JOIN pg_class child ON child.oid = inheritance.inhrelid
		WHERE parent_namespace.nspname = $1 AND parent.relname = $2`, schema, spec.Parent)
	if err != nil {
		return false, fmt.Errorf("inspect partitions for %s: %w", spec.Parent, err)
	}
	defer rows.Close()
	wanted := normalizedPartitionBound(lower, upper)
	newName := partitionName(spec, lower, upper)
	legacyName := fmt.Sprintf("%s_p_%d_%d", spec.Parent, lower, upper)
	for rows.Next() {
		var name, bound string
		if err := rows.Scan(&name, &bound); err != nil {
			return false, fmt.Errorf("scan partition for %s: %w", spec.Parent, err)
		}
		normalized := normalizePartitionBound(bound)
		if normalized == wanted {
			return true, nil
		}
		if name == newName || name == legacyName {
			return false, &PartitionRecoveryError{
				Table: spec.Parent, Lower: lower, Upper: upper,
				Cause: fmt.Sprintf("attached relation %s has unexpected bound %s", name, bound),
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate partitions for %s: %w", spec.Parent, err)
	}
	return false, nil
}

func validatePartialPartitionDependencies(
	ctx context.Context,
	tx *sql.Tx,
	schema string,
	pending []pendingPartition,
	existing map[string]bool,
	lower, upper uint64,
) error {
	missing := make(map[string]bool, len(pending))
	for _, partition := range pending {
		missing[partition.spec.Parent] = true
	}
	for _, spec := range blockPartitionSpecs {
		if !missing[spec.Parent] {
			continue
		}
		for _, dependent := range dependentPartitionTables(spec.Parent) {
			if !existing[dependent] {
				continue
			}
			var hasRows bool
			if err := tx.QueryRowContext(ctx, fmt.Sprintf(
				"SELECT EXISTS (SELECT 1 FROM %s WHERE block_number >= $1::numeric AND block_number < $2::numeric)",
				qualifiedIdentifier(schema, dependent),
			), decimal(lower), decimal(upper)).Scan(&hasRows); err != nil {
				return fmt.Errorf("inspect partial partition dependency %s: %w", dependent, err)
			}
			if hasRows {
				return &PartitionRecoveryError{
					Table: spec.Parent, Lower: lower, Upper: upper,
					Cause: fmt.Sprintf("existing dependent partition %s contains rows while its referenced partition is missing", dependent),
				}
			}
		}
	}
	return nil
}

func dependentPartitionTables(parent string) []string {
	var result []string
	for _, candidate := range blockPartitionSpecs {
		for _, dependency := range candidate.Dependencies {
			if dependency == parent {
				result = append(result, candidate.Parent)
			}
		}
	}
	return result
}

func relationExistsTx(ctx context.Context, tx *sql.Tx, schema, relation string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_class relation
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = $1 AND relation.relname = $2
		)`, schema, relation).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect partition relation %s: %w", relation, err)
	}
	return exists, nil
}

func partitionName(spec blockPartitionSpec, lower, upper uint64) string {
	return fmt.Sprintf("etherview_p_%s_%d_%d", spec.NameCode, lower, upper)
}

func normalizedPartitionBound(lower, upper uint64) string {
	return fmt.Sprintf("FORVALUESFROM(%d)TO(%d)", lower, upper)
}

func normalizePartitionBound(bound string) string {
	replacer := strings.NewReplacer(
		" ", "", "\n", "", "\r", "", "\t", "", "'", "", "::numeric", "",
	)
	return strings.ToUpper(replacer.Replace(bound))
}

func qualifiedIdentifier(schema, relation string) string {
	return quoteSQLIdentifier(schema) + "." + quoteSQLIdentifier(relation)
}

func quoteSQLIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
