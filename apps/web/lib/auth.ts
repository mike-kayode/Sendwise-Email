import { apiUrl } from "@/lib/api-url";
import { ApiError, normalizeFetchError } from "@/lib/api-errors";

const TOKEN_KEY = "access_token";
const ACCESS_TOKEN_EVENT = "sendwise:access-token";

export function getAccessToken(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function setAccessToken(token: string | null) {
  if (typeof window === "undefined") return;
  try {
    if (token) window.localStorage.setItem(TOKEN_KEY, token);
    else window.localStorage.removeItem(TOKEN_KEY);
  } catch {
    // Keep the auth store synchronized even when localStorage is unavailable.
  }
  window.dispatchEvent(new CustomEvent(ACCESS_TOKEN_EVENT, { detail: token }));
}

export function removeAccessToken() {
  setAccessToken(null);
}

let refreshInFlight: Promise<string | null> | null = null;

async function responseError(response: Response): Promise<ApiError> {
  let code = "request_failed";
  let message = "The request could not be completed.";
  try {
    const body = (await response.json()) as {
      error?: { code?: string; message?: string };
    };
    code = body.error?.code ?? code;
    message = body.error?.message ?? message;
  } catch {
    // Keep the generic error when the response is not a JSON error envelope.
  }
  return new ApiError(response.status, code, message);
}

async function fetchWithNetworkError(url: string, options: RequestInit): Promise<Response> {
  try {
    return await fetch(url, options);
  } catch (error) {
    throw normalizeFetchError(error);
  }
}

function isRefreshablePath(path: string): boolean {
  return ![
    "/auth/login",
    "/auth/register",
    "/auth/refresh",
    "/auth/logout",
    "/auth/verify",
    "/auth/resend-verification",
    "/auth/forgot-password",
    "/auth/reset-password",
  ].some((authPath) => path === authPath || path.startsWith(`${authPath}?`));
}

function shouldAttachAccessToken(path: string): boolean {
  return ![
    "/auth/login",
    "/auth/register",
    "/auth/refresh",
    "/auth/verify",
    "/auth/resend-verification",
    "/auth/forgot-password",
    "/auth/reset-password",
  ].some((authPath) => path === authPath || path.startsWith(`${authPath}?`));
}

async function refreshAccessToken(): Promise<string | null> {
  if (!refreshInFlight) {
    refreshInFlight = (async () => {
      const response = await fetchWithNetworkError(apiUrl("/api/v1/auth/refresh"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
        credentials: "include",
        cache: "no-store",
      });
      if (response.status === 401) {
        removeAccessToken();
        return null;
      }
      if (!response.ok) throw await responseError(response);

      const body = (await response.json()) as { access_token: string };
      if (!body.access_token) {
        throw new ApiError(502, "invalid_refresh_response", "The session could not be refreshed.");
      }
      setAccessToken(body.access_token);
      return body.access_token;
    })().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

export async function apiFetch<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const send = (token: string | null) => {
    const headers = new Headers(options.headers);
    if (!headers.has("Content-Type") && options.body && !(options.body instanceof FormData)) {
      headers.set("Content-Type", "application/json");
    }
    if (token) headers.set("Authorization", `Bearer ${token}`);
    else headers.delete("Authorization");
    return fetchWithNetworkError(apiUrl(`/api/v1${path}`), {
      ...options,
      headers,
      credentials: "include",
      cache: "no-store",
    });
  };

  const token = shouldAttachAccessToken(path) ? getAccessToken() : null;
  let response = await send(token);
  if (response.status === 401 && token && isRefreshablePath(path)) {
    const currentToken = getAccessToken();
    const refreshedToken =
      currentToken && currentToken !== token ? currentToken : await refreshAccessToken();
    if (refreshedToken) response = await send(refreshedToken);
    if (response.status === 401) {
      removeAccessToken();
      throw new ApiError(401, "unauthenticated", "You must sign in again.");
    }
  }
  if (!response.ok) {
    throw await responseError(response);
  }

  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export type ListResponse<T> = {
  data: T[];
  pagination: { total: number; limit: number; offset: number };
};

export type Contact = {
  id: string;
  organization_id?: string;
  email: string;
  first_name: string;
  last_name: string;
  status: string;
  custom_fields: Json;
  source?: string;
  tags?: Array<{ id: string; name: string; color: string }>;
  created_at: string;
  updated_at: string;
};

export type ContactImport = {
  id: string;
  status: string;
  file_name: string;
  total_rows: number;
  processed_rows: number;
  imported: number;
  skipped: number;
  invalid: number;
  errors: Array<{ row: number; message: string }>;
  last_error?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
};

export type Campaign = {
  id: string;
  organization_id?: string;
  name: string;
  subject: string;
  preview_text?: string;
  sender_name: string;
  sender_email: string;
  sender_postal_address?: string;
  alerts?: { type: string; message: string; created_at: string }[];
  reply_to?: string;
  list_id?: string;
  segment_id?: string;
  template_id?: string;
  content: Json;
  status: string;
  recipient_count: number;
  track_opens: boolean;
  track_clicks: boolean;
  scheduled_at?: string;
  started_at?: string;
  completed_at?: string;
  paused_at?: string;
  created_at: string;
  updated_at: string;
};

export type Json = Record<string, unknown> | null;

export type SendingDomain = {
  id: string;
  domain: string;
  status: string;
  stalwart_authenticated?: boolean;
  dkim_selector?: string;
  spf_status?: string;
  dkim_status?: string;
  dmarc_status?: string;
  verification_status?: string;
  last_checked_at?: string;
  verified_at?: string;
  created_at: string;
};

export type AnalyticsOverview = {
  contacts: number;
  subscribed: number;
  campaigns_sent: number;
  emails_submitted: number;
  opens: number;
  clicks: number;
  bounces: number;
  complaints: number;
  unsubscribes: number;
  open_rate: number;
  click_rate: number;
  bounce_rate: number;
};

export type DomainDNSRecord = {
  type: string;
  name: string;
  value: string;
  purpose: string;
  status: string;
};

export type CampaignReport = {
  campaign?: Campaign;
  recipients: { total: number; by_status: Record<string, number> };
  events: { by_type: Record<string, number> };
  rates: { open_rate: number; click_rate: number; bounce_rate: number };
  top_links: { url: string; clicks: number }[];
};

export type ValidationCheck = {
  name: string;
  status: string; // "pass" | "warning" | "error"
  message: string;
};

export type CampaignValidation = {
  valid: boolean;
  checks: ValidationCheck[];
};

export type DomainDetail = SendingDomain & {
  dns_records: DomainDNSRecord[];
};

export type DeadLetterItem = {
  id: string;
  organization_id: string;
  campaign_id: string;
  contact_id: string;
  delivery_job_id: string;
  reason: string;
  attempts: number;
  last_error: string;
  created_at: string;
  replayed_at?: string;
};

export type SuppressionItem = {
  email: string;
  reason: string;
  source: string;
  organization_id: string;
  created_at: string;
};

export type Organization = {
  id: string;
  name: string;
  slug: string;
  created_at: string;
};

export type Membership = {
  user_id: string;
  organization_id: string;
  role: string;
  created_at: string;
};

export type MembershipWithOrganization = {
  membership: Membership;
  organization: Organization;
};

export type Invitation = {
  id: string;
  organization_id: string;
  email: string;
  role: string;
  status: string;
  invited_by?: string;
  expires_at: string;
  accepted_at?: string;
  created_at: string;
};