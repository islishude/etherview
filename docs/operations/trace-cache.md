# Trace cache storage

[Operations index](../operations.md) · Commands run from the repository root.

- [RustFS trace-cache deployment](#rustfs-trace-cache-deployment)
- [S3 trace-cache credentials](#s3-trace-cache-credentials)

## RustFS trace-cache deployment

The optional `accelerators` Compose profile uses `rustfs/rustfs:1.0.5` as
`object-storage`. S3 listens on container port 9000 and the console on 9001;
production Compose publishes neither port. RustFS runs as the image's default
non-root user, with a new `rustfs-data` named volume. The `/health/ready` probe
checks the store itself, never application readiness.

When upgrading from 1.0.4, retain the existing volume and recreate
`object-storage` with the pinned image. For multi-node deployments, the
[1.0.5 release notes](https://github.com/rustfs/rustfs/releases/tag/1.0.5) require
upgrading every node before pool changes or decommission retries. DELETE
requests may return HTTP 503 while versions coexist; retry after all nodes
are upgraded.

To enable a local trace cache:

1. Copy the deployment environment example to a private environment file and
   set `RUSTFS_ACCESS_KEY` and `RUSTFS_SECRET_KEY` to your chosen credentials.
   Keep `ETHERVIEW_S3_ENDPOINT` unset until the bucket exists.
2. Start only the object store through the repository wrapper:
   `.github/scripts/compose.sh --project-directory . -f deploy/compose.yaml --env-file /absolute/path/to/compose.env --profile accelerators up -d --wait object-storage`.
3. Provision `etherview-cache` with an S3 client on the Compose network, or
   temporarily publish the console using this local override:

   ```yaml
   services:
     object-storage:
       ports:
         - "127.0.0.1:9001:9001"
   ```

   Save the override outside tracked files and start with both
   `--project-directory . -f deploy/compose.yaml -f /absolute/path/to/rustfs-console.yaml`,
   retaining the same `--env-file` and profile arguments. Open the local
   console at `http://127.0.0.1:9001`, sign in with the RustFS credentials, and
   create the bucket. Recreate the service without the override afterward.

4. Set `ETHERVIEW_S3_ENDPOINT=http://object-storage:9000`,
   `ETHERVIEW_S3_BUCKET=etherview-cache`, `ETHERVIEW_S3_REGION=us-east-1`, and
   `ETHERVIEW_S3_PATH_STYLE=true`. Set the explicit Etherview access/secret pair
   to matching RustFS credentials. Recreate the API-capable service using the
   same environment file and the chosen `monolith` or `distributed` profile.
5. Request an indexed transaction trace and verify that an object appears in
   the bucket and subsequent reads succeed. The cache fills only for eligible
   completed trace generations; PostgreSQL remains authoritative.

When replacing MinIO, the old `object-data` Docker volume remains untouched.
Do not mount it into RustFS or run volume-pruning commands as part of the
switch. No object migration is needed: cache misses read PostgreSQL and write
fresh RustFS objects. To bypass RustFS, unset the application S3 endpoint and
bucket and recreate the API-capable service. Retain both volumes; disabling
this accelerator does not require restoring database data.

## S3 trace-cache credentials

The S3-compatible trace cache is enabled only when `adapters.s3_endpoint` and
`adapters.s3_bucket` are set. A complete
`ETHERVIEW_S3_ACCESS_KEY(_FILE)`/`ETHERVIEW_S3_SECRET_KEY(_FILE)` pair plus an
optional session token is an explicit highest-precedence override. When that
pair is absent, only `all`/`api` loads the AWS SDK default chain: AWS
environment credentials, Web Identity, shared configuration/profile, container
credentials, then EC2 instance role. Temporary credentials are cached and
refreshed by the SDK without a process restart.

With both explicit credentials and `ETHERVIEW_S3_REGION`, unrelated missing or
malformed AWS profiles are ignored. If the explicit region is omitted, AWS
configuration is still loaded for region discovery. Credential endpoint and
STS HTTP redirects are rejected; configure their final endpoints directly.

Empty explicit keys never mean anonymous access. Missing, expired, malformed,
or unreachable credentials produce a bounded redacted cache failure and the
request uses the PostgreSQL trace projection. They do not withdraw readiness.
Object transport uses AWS SDK for Go v2 with a single request attempt and no
HTTP redirects. Region precedence is `adapters.s3_region`, AWS environment/shared
configuration, then `us-east-1`. Configure the actual bucket region when using
AWS S3 outside `us-east-1`; there is no automatic bucket-region lookup.
The existing `adapters.operation_timeout` bounds both credential retrieval and
the object operation. A malformed default-chain configuration may fail API
startup as a static configuration error; ordinary absence remains lazy until
the first cache operation.

Compose passes explicit Etherview and standard AWS credential inputs only to
the `all`/`api` service. File paths named by AWS variables must be mounted into
the container with the same absolute path. For Helm, enable the dedicated
`s3ServiceAccount` instead of annotating the shared account. IRSA uses the
dedicated account's annotations. For EKS Pod Identity, create the AWS
association separately and set `eksPodIdentity=true`; the chart then permits
only the API-capable Pod to reach the fixed EKS agent addresses on TCP/80.
Other roles retain no S3 Secret or credential-agent egress. Keep EC2 IMDS
blocked for Kubernetes workloads.

If IRSA must call STS while `networkPolicy.allowExternalHTTPS=false`, place
only reviewed regional STS CIDRs in `networkPolicy.runtimeHTTPSCIDRs`. Do not
add internet-wide or link-local ranges. After changing credential source,
restart the `all`/`api` Deployment and confirm trace-cache hits while
PostgreSQL remains authoritative.
