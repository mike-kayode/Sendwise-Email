import assert from "node:assert/strict";
import test from "node:test";
import { apiUrl } from "../lib/api-url.ts";
import { ApiError, normalizeFetchError } from "../lib/api-errors.ts";
import {
  loginErrorMessage,
  loginRequest,
  prepareLoginLocation,
} from "../lib/login-utils.ts";

test("apiUrl uses same-origin paths by default and normalizes configured bases", () => {
  assert.equal(apiUrl("/api/v1/auth/login", ""), "/api/v1/auth/login");
  assert.equal(apiUrl("/api/v1/auth/login", "https://campaign.pipsleague.com/"), "https://campaign.pipsleague.com/api/v1/auth/login");
});

test("login location removes all query values while retaining a safe callback", () => {
  const location = prepareLoginLocation(
    "/login",
    "?email=person%40example.com&password=never-put-this-in-a-url&callbackUrl=%2Fdashboard%2Fsettings",
  );
  assert.deepEqual(location, {
    callbackUrl: "/dashboard/settings",
    cleanUrl: "/login",
  });
  assert.equal(JSON.stringify(location).includes("never-put-this-in-a-url"), false);
});

test("login submits credentials only in a POST JSON body with cookies enabled", () => {
  const request = loginRequest("person@example.com", "example-password");
  assert.equal(request.method, "POST");
  assert.equal(request.credentials, "include");
  assert.deepEqual(JSON.parse(request.body), {
    email: "person@example.com",
    password: "example-password",
  });
  assert.equal("url" in request, false);
});

test("network fetch failures become a clear login message without hiding HTTP errors", () => {
  const networkError = normalizeFetchError(new TypeError("Failed to fetch"));
  assert.ok(networkError instanceof ApiError);
  assert.equal(networkError.code, "network_error");
  assert.equal(
    loginErrorMessage(networkError),
    "Cannot reach the server. Please check your connection and try again.",
  );
  assert.equal(
    loginErrorMessage(
      new ApiError(401, "invalid_credentials", "The email address or password is incorrect."),
    ),
    "The email address or password is incorrect.",
  );
});
