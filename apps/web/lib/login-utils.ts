export function loginRequest(email: string, password: string): RequestInit {
  return {
    method: "POST",
    body: JSON.stringify({ email, password }),
    credentials: "include",
  };
}

export function prepareLoginLocation(pathname: string, search: string): {
  callbackUrl: string;
  cleanUrl: string;
} {
  const callbackUrl = new URLSearchParams(search).get("callbackUrl");
  return {
    callbackUrl: callbackUrl?.startsWith("/") && !callbackUrl.startsWith("//")
      ? callbackUrl
      : "/dashboard",
    cleanUrl: pathname,
  };
}

export function loginErrorMessage(error: unknown): string {
  if (
    typeof error === "object" &&
    error !== null &&
    "code" in error
  ) {
    const code = (error as { code?: string }).code;
    switch (code) {
      case "network_error":
        return "Cannot reach the server. Please check your connection and try again.";
      case "email_not_verified":
        return "Please verify your email address before signing in. Check your inbox for a verification link.";
      case "invalid_credentials":
        return "The email address or password is incorrect.";
      case "workspace_pending":
        return "Your workspace is still being set up. Please try again shortly.";
      case "workspace_unavailable":
        return "Your workspace is temporarily unavailable. Please try again later.";
      default:
        if (error instanceof Error) return error.message;
    }
  }
  if (error instanceof Error) return error.message;
  return "Could not sign in. Please try again.";
}
