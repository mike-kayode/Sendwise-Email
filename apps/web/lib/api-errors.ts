export class ApiError extends Error {
  status: number;
  code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export function normalizeFetchError(error: unknown): unknown {
  if (error instanceof TypeError) {
    return new ApiError(0, "network_error", "Cannot reach the server. Please try again.");
  }
  return error;
}
