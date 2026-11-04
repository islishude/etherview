# Web development

[Development index](../development.md).

Run these commands from the repository root. Vite serves only the frontend;
both `dev` and `preview` require a running Etherview API. Their `/api` proxy in
[`web/vite.config.ts`](../../web/vite.config.ts) targets <http://localhost:8080>.

To provide that backend, use the external-chain deployment
in a separate terminal (Docker is required):

```sh
# First-time setup; keep an existing .env.deploy.
cp deploy/compose.env.example .env.deploy
# Edit .env.deploy: set POSTGRES_PASSWORD and ETHERVIEW_RPC_URLS,
# match ETHERVIEW_CHAIN_ID and ETHERVIEW_CHAIN_GENESIS_HASH to that chain,
# and keep ETHERVIEW_PORT=8080.
docker compose --project-directory . -f deploy/compose.yaml --env-file .env.deploy --profile monolith up --build
```

This starts PostgreSQL, applies migrations, and runs all backend roles. The RPC
endpoint must be reachable from the container; `localhost` there refers to the
container itself. See the [deployment guide](../../deploy/README.md#docker-compose)
for configuration details. Once the backend is ready, check it and start Vite
in another terminal:

```sh
curl -fsS http://localhost:8080/api/v1/config
pnpm install --frozen-lockfile
pnpm --dir web run dev
```

Open <http://127.0.0.1:5173> for hot reload. For example, the browser requests
`http://127.0.0.1:5173/api/v1/config`, and Vite forwards it to
`http://localhost:8080/api/v1/config`; the browser client stays same-origin.
If Vite reports `ECONNREFUSED`, check that the backend is running and its
published port matches the proxy target. If you change the backend port, update
both `server.proxy` and `preview.proxy` in `web/vite.config.ts` and restart Vite.

To inspect the production bundle, keep the backend running and run:

```sh
pnpm --dir web run build
pnpm --dir web run preview
```

Open <http://127.0.0.1:4173>. Rebuild after source changes; `preview` serves the
existing build and does not provide hot reload or start the backend.

Vite `preview` is separate from `make start-preview`. The latter starts the
[full-stack Preview](../../preview/docs/README.md) at
<https://app.etherview.localhost> and does not publish the API on host port 8080,
so it does not satisfy Vite's default proxy target. Use that HTTPS site directly
to inspect the full-stack Preview's bundled frontend.

If you enable wallet authentication on the development backend, set
`server.public_url` in the mounted YAML configuration (by default,
`deploy/config.example.yaml`) to the browser origin you use
(`http://127.0.0.1:5173` for `dev`, or `http://127.0.0.1:4173` for `preview`)
and restart the backend to load the changed configuration. Follow the
[authentication setup](../../deploy/README.md#docker-compose) for the required feature
and secret settings; `localhost` and `127.0.0.1` are different origins.
