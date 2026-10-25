import type { ReactNode } from "react";
import { AuthProvider } from "@/auth/AuthProvider";
import { WalletProvider } from "@/wallet/WalletProvider";

export const verificationCSRF = "c".repeat(43);
export const verificationSession = {
  authenticated: true,
  csrf_token: verificationCSRF,
  expires_at: "2099-01-01T00:00:00Z",
  user: {
    id: "018f3b52-0b3d-7bf1-b65f-6f214827cb41",
    chain_id: "1",
    address: "0x1111111111111111111111111111111111111111",
    role: "user",
    status: "active",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
};
export function withVerificationSession(fetcher: typeof fetch): typeof fetch {
  return async (input, init) => {
    if (String(input) === "/api/v1/auth/session")
      return Response.json({
        data: verificationSession,
        meta: { chain_id: "1", request_id: "session" },
      });
    const response = await fetcher(input, init);
    if (String(input) !== "/api/v1/config") return response;
    const body = await response.json();
    body.data.features.user_auth = true;
    return Response.json(body);
  };
}
export function VerificationTestSession({ children }: { children: ReactNode }) {
  return (
    <WalletProvider>
      <AuthProvider>{children}</AuthProvider>
    </WalletProvider>
  );
}
