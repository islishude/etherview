-- name: EnrichSavepointDispatchJobs :exec
SAVEPOINT enrichment_dispatch_jobs;

-- name: EnrichRollbackDispatchJobs :exec
ROLLBACK TO SAVEPOINT enrichment_dispatch_jobs;

-- name: EnrichReleaseDispatchJobs :exec
RELEASE SAVEPOINT enrichment_dispatch_jobs;

-- name: EnrichSavepointStageOutput :exec
SAVEPOINT enrichment_stage_output;

-- name: EnrichRollbackStageOutput :exec
ROLLBACK TO SAVEPOINT enrichment_stage_output;
