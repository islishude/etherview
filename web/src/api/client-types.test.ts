import { expectTypeOf, it } from "vitest";
import * as sdk from "./heyapi/sdk.gen";
import type * as schemas from "./heyapi/types.gen";
import { apiClient, sameOriginAPIPath } from "./client";
import type { TransactionDetail } from "./types";

it("preserves discriminants, exact quantities, nullability and schema constraints", () => {
  expectTypeOf<schemas.AddressVerificationSubmission["input"]>().toEqualTypeOf<
    Record<string, unknown> | undefined
  >();
  expectTypeOf<schemas.Quantity>().toEqualTypeOf<string>();
  expectTypeOf<
    Extract<TransactionDetail, { kind: "included" }>["transaction"]
  >().toEqualTypeOf<schemas.Transaction>();
  expectTypeOf<
    Extract<TransactionDetail, { kind: "pending" }>["transaction"]
  >().toEqualTypeOf<schemas.PendingTransaction>();
  expectTypeOf<
    Extract<TransactionDetail, { kind: "replaced" }>["replacement_hash"]
  >().toEqualTypeOf<string>();
  expectTypeOf<schemas.User["display_name"]>().toEqualTypeOf<string | null | undefined>();
  expectTypeOf<schemas.UserApiKeyPolicy["allowed_scopes"]>().toEqualTypeOf<
    [schemas.ApiKeyScope, schemas.ApiKeyScope]
  >();
});

// Compiled by tsc, never invoked: invalid SDK calls must remain type errors.
export function rejectedRequests() {
  // @ts-expect-error A configured project client is mandatory.
  sdk.getStatus({});
  // @ts-expect-error Unknown SSE paths are not accepted.
  sameOriginAPIPath("/made-up");
  // @ts-expect-error Invalid path parameters.
  sdk.getBlock({ client: apiClient, path: { hash: "0x12" } });
  // @ts-expect-error Required path is missing.
  sdk.getBlock({ client: apiClient });
  // @ts-expect-error Limits are numbers, not strings.
  sdk.listBlocks({ client: apiClient, query: { limit: "25" } });
  // @ts-expect-error Unknown query parameters, including URL credentials.
  sdk.listBlocks({ client: apiClient, query: { apikey: "secret" } });
  // @ts-expect-error Read operations cannot submit bodies.
  sdk.getStatus({ client: apiClient, body: {} });
  // @ts-expect-error Challenge requests require an address.
  sdk.createAuthChallenge({ client: apiClient, body: {} });
  // @ts-expect-error Required CSRF header cannot be omitted.
  sdk.logoutAuthSession({ client: apiClient });
  sdk.createBillingTopupIntent({
    client: apiClient,
    headers: { "X-CSRF-Token": "csrf" },
    // @ts-expect-error Billing quantities must not be coerced to numbers.
    body: { amount_atomic: 123 },
  });
  // @ts-expect-error Enum values remain closed.
  const scope: schemas.ApiKeyScope = "administrator";
  // @ts-expect-error Required discriminated fields cannot be omitted.
  const replaced: TransactionDetail = { kind: "replaced", transaction: {} };
  return { scope, replaced };
}
