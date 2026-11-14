"use client";

import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, Tbody, Td, Th, Thead, Tr } from "@/components/ui/table";
import { apiFetch } from "@/lib/auth";
import { formatDate } from "@/lib/utils";

type Settings = {
  enabled: boolean;
  status: string;
  pause_reason: string;
  daily_limit: number;
  monthly_limit: number;
  max_recipients: number;
  max_message_bytes: number;
};

type APIKey = {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  test_mode: boolean;
  last_used_at?: string;
  expires_at?: string;
  grace_until?: string;
  revoked_at?: string;
  created_at: string;
  plain_key?: string;
};

type Webhook = {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
  consecutive_failures: number;
  disabled_at?: string;
  created_at: string;
  signing_secret?: string;
};

type SMTPCredential = {
  id: string;
  name: string;
  username: string;
  requests_per_second: number;
  last_used_at?: string;
  revoked_at?: string;
  created_at: string;
  password?: string;
};

type Message = {
  id: string;
  message_id: string;
  status: string;
  from: string;
  subject: string;
  created_at: string;
};

type Usage = {
  accepted_today: number;
  accepted_this_month: number;
  delivered_this_month: number;
  bounced_this_month: number;
  complained_this_month: number;
  daily_limit: number;
  monthly_limit: number;
};

const webhookEventOptions = [
  "message.delivered",
  "message.bounced",
  "message.complained",
  "message.failed",
  "message.deferred",
  "message.sent",
];

export default function TransactionalPage() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [webhooks, setWebhooks] = useState<Webhook[]>([]);
  const [smtpCredentials, setSMTPCredentials] = useState<SMTPCredential[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [usage, setUsage] = useState<Usage | null>(null);
  const [keyName, setKeyName] = useState("");
  const [smtpCredentialName, setSMTPCredentialName] = useState("");
  const [smtpUsername, setSMTPUsername] = useState("");
  const [smtpPassword, setSMTPPassword] = useState("");
  const [testMode, setTestMode] = useState(true);
  const [webhookURL, setWebhookURL] = useState("");
  const [webhookEvents, setWebhookEvents] = useState<string[]>(["message.delivered", "message.bounced"]);
  const [oneTimeSecret, setOneTimeSecret] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const loadData = useCallback(async () => {
    setError("");
    try {
      const [settingsResult, keyResult, smtpResult, webhookResult, messageResult, usageResult] = await Promise.all([
        apiFetch<Settings>("/transactional/settings"),
        apiFetch<{ data: APIKey[] }>("/transactional/api-keys"),
        apiFetch<{ data: SMTPCredential[] }>("/transactional/smtp-credentials"),
        apiFetch<{ data: Webhook[] }>("/transactional/webhooks"),
        apiFetch<{ data: Message[] }>("/transactional/dashboard/messages?limit=20"),
        apiFetch<Usage>("/transactional/usage"),
      ]);
      setSettings(settingsResult);
      setKeys(keyResult.data ?? []);
      setSMTPCredentials(smtpResult.data ?? []);
      setWebhooks(webhookResult.data ?? []);
      setMessages(messageResult.data ?? []);
      setUsage(usageResult);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Could not load transactional email data.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  async function updateSettings(enabled: boolean) {
    setBusy(true);
    try {
      const updated = await apiFetch<Settings>("/transactional/settings", {
        method: "PUT",
        body: JSON.stringify({ enabled, status: enabled ? "active" : "disabled" }),
      });
      setSettings(updated);
      await loadData();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "Could not update transactional settings.");
    } finally {
      setBusy(false);
    }
  }

  async function createKey() {
    const name = keyName.trim();
    if (!name || busy) return;
    setBusy(true);
    setError("");
    try {
      const created = await apiFetch<APIKey>("/transactional/api-keys", {
        method: "POST",
        body: JSON.stringify({ name, scopes: ["send", "read"], test_mode: testMode }),
      });
      setOneTimeSecret(created.plain_key ?? "");
      setKeyName("");
      await loadData();
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "Could not create API key.");
    } finally {
      setBusy(false);
    }
  }

  async function rotateKey(id: string) {
    setBusy(true);
    setError("");
    try {
      const rotated = await apiFetch<APIKey>(`/transactional/api-keys/${id}/rotate`, { method: "POST" });
      setOneTimeSecret(rotated.plain_key ?? "");
      await loadData();
    } catch (rotateError) {
      setError(rotateError instanceof Error ? rotateError.message : "Could not rotate API key.");
    } finally {
      setBusy(false);
    }
  }

  async function revokeKey(id: string) {
    setBusy(true);
    setError("");
    try {
      await apiFetch(`/transactional/api-keys/${id}`, { method: "DELETE" });
      await loadData();
    } catch (revokeError) {
      setError(revokeError instanceof Error ? revokeError.message : "Could not revoke API key.");
    } finally {
      setBusy(false);
    }
  }

  async function createSMTPCredential() {
    const name = smtpCredentialName.trim();
    if (!name || busy) return;
    setBusy(true);
    setError("");
    try {
      const created = await apiFetch<SMTPCredential>("/transactional/smtp-credentials", {
        method: "POST",
        body: JSON.stringify({ name }),
      });
      setSMTPUsername(created.username);
      setSMTPPassword(created.password ?? "");
      setSMTPCredentialName("");
      await loadData();
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "Could not create SMTP credential.");
    } finally {
      setBusy(false);
    }
  }

  async function revokeSMTPCredential(id: string) {
    setBusy(true);
    setError("");
    try {
      await apiFetch(`/transactional/smtp-credentials/${id}`, { method: "DELETE" });
      await loadData();
    } catch (revokeError) {
      setError(revokeError instanceof Error ? revokeError.message : "Could not revoke SMTP credential.");
    } finally {
      setBusy(false);
    }
  }

  async function createWebhook() {
    if (!webhookURL.trim() || webhookEvents.length === 0 || busy) return;
    setBusy(true);
    setError("");
    try {
      const created = await apiFetch<Webhook>("/transactional/webhooks", {
        method: "POST",
        body: JSON.stringify({ url: webhookURL.trim(), events: webhookEvents }),
      });
      setOneTimeSecret(created.signing_secret ?? "");
      setWebhookURL("");
      await loadData();
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "Could not create webhook.");
    } finally {
      setBusy(false);
    }
  }

  async function rotateWebhookSecret(id: string) {
    setBusy(true);
    setError("");
    try {
      const result = await apiFetch<{ signing_secret: string }>(
        `/transactional/webhooks/${id}/rotate-secret`,
        { method: "POST" },
      );
      setOneTimeSecret(result.signing_secret);
      await loadData();
    } catch (rotateError) {
      setError(rotateError instanceof Error ? rotateError.message : "Could not rotate webhook secret.");
    } finally {
      setBusy(false);
    }
  }

  async function deleteWebhook(id: string) {
    setBusy(true);
    setError("");
    try {
      await apiFetch(`/transactional/webhooks/${id}`, { method: "DELETE" });
      await loadData();
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "Could not delete webhook.");
    } finally {
      setBusy(false);
    }
  }

  function toggleWebhookEvent(event: string) {
    setWebhookEvents((current) =>
      current.includes(event) ? current.filter((value) => value !== event) : [...current, event],
    );
  }

  if (loading) {
    return <PageHeader title="Transactional email" description="Loading workspace delivery data..." />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Transactional email"
        description="Manage opt-in transactional sending, API credentials, delivery notifications, and live usage."
      />
      {error ? (
        <p role="alert" className="rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-800">
          {error}
        </p>
      ) : null}
      {oneTimeSecret ? (
        <Card className="border-emerald-300 bg-emerald-50">
          <CardHeader>
            <CardTitle className="text-base">Copy this secret now</CardTitle>
            <CardDescription>This secret is shown once and cannot be retrieved later.</CardDescription>
          </CardHeader>
          <CardContent>
            <code className="block break-all rounded bg-white p-3 text-sm">{oneTimeSecret}</code>
            <Button className="mt-3" variant="outline" onClick={() => setOneTimeSecret("")}>
              Dismiss
            </Button>
          </CardContent>
        </Card>
      ) : null}
      {smtpPassword ? (
        <Card className="border-emerald-300 bg-emerald-50" data-testid="smtp-one-time-credential">
          <CardHeader>
            <CardTitle className="text-base">Copy these SMTP credentials now</CardTitle>
            <CardDescription>The password is shown once and cannot be retrieved later.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            <p className="text-sm"><span className="font-medium">Username:</span> <code>{smtpUsername}</code></p>
            <p className="break-all text-sm"><span className="font-medium">Password:</span> <code>{smtpPassword}</code></p>
            <Button className="mt-2" variant="outline" onClick={() => { setSMTPUsername(""); setSMTPPassword(""); }}>
              Dismiss
            </Button>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 md:grid-cols-4">
        {[
          ["Accepted today", usage?.accepted_today, settings?.daily_limit],
          ["Accepted this month", usage?.accepted_this_month, settings?.monthly_limit],
          ["Delivered this month", usage?.delivered_this_month, undefined],
          ["Bounced / complaints", `${usage?.bounced_this_month ?? 0} / ${usage?.complained_this_month ?? 0}`, undefined],
        ].map(([label, value, limit]) => (
          <Card key={String(label)}>
            <CardHeader className="pb-2">
              <CardDescription>{label}</CardDescription>
              <CardTitle className="text-2xl tabular-nums">
                {value ?? 0}
                {limit === undefined ? "" : ` / ${limit}`}
              </CardTitle>
            </CardHeader>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-4">
          <div>
            <CardTitle>Workspace sending</CardTitle>
            <CardDescription>
              Sending is opt-in. Messages require a verified transactional sending domain.
              {settings?.pause_reason ? ` ${settings.pause_reason}` : ""}
            </CardDescription>
          </div>
          <Badge variant={settings?.status === "active" ? "success" : "neutral"}>
            {settings?.status ?? "unknown"}
          </Badge>
        </CardHeader>
        <CardContent>
          <Button
            disabled={busy || !settings}
            variant={settings?.enabled ? "outline" : "primary"}
            onClick={() => void updateSettings(!settings?.enabled)}
          >
            {settings?.enabled ? "Disable transactional sending" : "Enable transactional sending"}
          </Button>
          <p className="mt-3 text-xs text-slate-500">
            Limits: {settings?.max_recipients ?? 0} recipients per message;{" "}
            {Math.round((settings?.max_message_bytes ?? 0) / 1024 / 1024)} MiB maximum message size.
          </p>
        </CardContent>
      </Card>

      <div className="grid gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>SMTP submission credentials</CardTitle>
            <CardDescription>
              Use these credentials with STARTTLS on port 587 or implicit TLS on port 465. Authentication always requires encryption.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap gap-2">
              <Input
                aria-label="SMTP credential name"
                value={smtpCredentialName}
                onChange={(event) => setSMTPCredentialName(event.target.value)}
                placeholder="Credential name"
              />
              <Button disabled={busy || !smtpCredentialName.trim()} onClick={() => void createSMTPCredential()}>
                Create SMTP credential
              </Button>
            </div>
            <div className="divide-y divide-slate-100">
              {smtpCredentials.length === 0 ? <p className="py-3 text-sm text-slate-500">No SMTP credentials.</p> : null}
              {smtpCredentials.map((credential) => (
                <div key={credential.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{credential.name}</p>
                    <p className="text-xs text-slate-500">
                      {credential.username} · {credential.requests_per_second} messages/second
                      {credential.revoked_at ? " · revoked" : ""}
                    </p>
                  </div>
                  {!credential.revoked_at ? (
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => void revokeSMTPCredential(credential.id)}>
                      Revoke
                    </Button>
                  ) : null}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>API keys</CardTitle>
            <CardDescription>Keys are stored as hashes. Rotate to issue a replacement with a 24-hour grace period.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap gap-2">
              <Input value={keyName} onChange={(event) => setKeyName(event.target.value)} placeholder="Key name" />
              <label className="flex items-center gap-2 text-sm text-slate-600">
                <input type="checkbox" checked={testMode} onChange={(event) => setTestMode(event.target.checked)} />
                Test mode
              </label>
              <Button disabled={busy || !keyName.trim()} onClick={() => void createKey()}>Create key</Button>
            </div>
            <div className="divide-y divide-slate-100">
              {keys.length === 0 ? <p className="py-3 text-sm text-slate-500">No transactional API keys.</p> : null}
              {keys.map((key) => (
                <div key={key.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{key.name}</p>
                    <p className="text-xs text-slate-500">
                      {key.key_prefix} · {key.scopes.join(", ")} · {key.test_mode ? "test" : "live"}
                      {key.revoked_at ? " · revoked" : ""}
                    </p>
                  </div>
                  {!key.revoked_at ? (
                    <div className="flex gap-2">
                      <Button size="sm" variant="outline" disabled={busy} onClick={() => void rotateKey(key.id)}>Rotate</Button>
                      <Button size="sm" variant="outline" disabled={busy} onClick={() => void revokeKey(key.id)}>Revoke</Button>
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Webhooks</CardTitle>
            <CardDescription>HTTPS endpoints receive signed events. Private and reserved destination addresses are blocked.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <Input value={webhookURL} onChange={(event) => setWebhookURL(event.target.value)} placeholder="https://example.com/sendwise-events" />
            <div className="flex flex-wrap gap-x-4 gap-y-2">
              {webhookEventOptions.map((event) => (
                <label key={event} className="flex items-center gap-2 text-xs text-slate-600">
                  <input type="checkbox" checked={webhookEvents.includes(event)} onChange={() => toggleWebhookEvent(event)} />
                  {event}
                </label>
              ))}
            </div>
            <Button disabled={busy || !webhookURL.trim() || webhookEvents.length === 0} onClick={() => void createWebhook()}>
              Add webhook
            </Button>
            <div className="divide-y divide-slate-100">
              {webhooks.length === 0 ? <p className="py-3 text-sm text-slate-500">No webhook endpoints.</p> : null}
              {webhooks.map((webhook) => (
                <div key={webhook.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{webhook.url}</p>
                    <p className="text-xs text-slate-500">
                      {webhook.events.join(", ")} · {webhook.enabled ? "enabled" : "disabled"}
                      {webhook.consecutive_failures ? ` · ${webhook.consecutive_failures} consecutive failures` : ""}
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => void rotateWebhookSecret(webhook.id)}>Rotate secret</Button>
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => void deleteWebhook(webhook.id)}>Delete</Button>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Recent messages</CardTitle>
          <CardDescription>Latest messages in this workspace, with their current recipient-level aggregate status.</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <Thead>
              <Tr><Th>Subject</Th><Th>From</Th><Th>Status</Th><Th>Created</Th></Tr>
            </Thead>
            <Tbody>
              {messages.map((message) => (
                <Tr key={message.id}>
                  <Td className="font-medium">{message.subject}</Td>
                  <Td>{message.from}</Td>
                  <Td><Badge variant="neutral">{message.status}</Badge></Td>
                  <Td>{formatDate(message.created_at)}</Td>
                </Tr>
              ))}
              {messages.length === 0 ? (
                <Tr><Td colSpan={4} className="py-6 text-center text-slate-500">No transactional messages yet.</Td></Tr>
              ) : null}
            </Tbody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
