"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input, Select } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { apiFetch, type Contact } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";

export function AddContactDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onCreated?: (contact: Contact) => void;
}) {
  const queryClient = useQueryClient();
  const { toast } = useToast();

  const [email, setEmail] = useState("");
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [status, setStatus] = useState<"subscribed" | "pending">("subscribed");
  const [source, setSource] = useState("manual entry in dashboard");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setEmail("");
    setFirstName("");
    setLastName("");
    setStatus("subscribed");
    setSource("manual entry in dashboard");
    setError(null);
  };

  const close = (next: boolean) => {
    onOpenChange?.(next);
    if (!next) reset();
  };

  const handleSubmit = async () => {
    if (!email.trim() || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        email: email.trim(),
        first_name: firstName.trim(),
        last_name: lastName.trim(),
        status,
        source: source.trim() || "manual entry in dashboard",
      };
      if (status === "subscribed") {
        body.consent = {
          source: source.trim() || "manual entry in dashboard",
          purpose: "marketing",
          evidence: {
            method: "manual_entry",
            recorded_at: new Date().toISOString(),
            context: "Contact created from the dashboard Audience > Contacts page.",
          },
        };
      }
      const contact = await apiFetch<Contact>("/contacts", {
        method: "POST",
        body: JSON.stringify(body),
      });
      await queryClient.invalidateQueries({ queryKey: ["contacts"] });
      toast({
        title: "Contact added",
        description:
          status === "subscribed"
            ? `${contact.email} is subscribed.`
            : `${contact.email} was added as pending.`,
      });
      onCreated?.(contact);
      close(false);
    } catch (err: any) {
      const message = err?.message ?? "Unknown error";
      setError(message);
      toast({ title: "Could not add contact", description: message, variant: "destructive" });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogTitle className="text-base font-semibold text-slate-900">Add contact</DialogTitle>
        <DialogDescription>
          Add a single person to your audience. Subscribed contacts require consent evidence and
          cannot be on the suppression list.
        </DialogDescription>

        <div className="space-y-3">
          <div>
            <label htmlFor="contact-email" className="mb-1.5 block text-sm font-medium text-slate-700">
              Email address <span className="text-rose-500">*</span>
            </label>
            <Input
              id="contact-email"
              type="email"
              placeholder="person@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void handleSubmit();
              }}
            />
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label htmlFor="contact-first" className="mb-1.5 block text-sm font-medium text-slate-700">
                First name
              </label>
              <Input
                id="contact-first"
                value={firstName}
                onChange={(e) => setFirstName(e.target.value)}
                placeholder="Ada"
              />
            </div>
            <div>
              <label htmlFor="contact-last" className="mb-1.5 block text-sm font-medium text-slate-700">
                Last name
              </label>
              <Input
                id="contact-last"
                value={lastName}
                onChange={(e) => setLastName(e.target.value)}
                placeholder="Lovelace"
              />
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label htmlFor="contact-status" className="mb-1.5 block text-sm font-medium text-slate-700">
                Status
              </label>
              <Select
                id="contact-status"
                value={status}
                onChange={(e) => setStatus(e.target.value as "subscribed" | "pending")}
              >
                <option value="subscribed">Subscribed (with consent)</option>
                <option value="pending">Pending (confirmation needed)</option>
              </Select>
            </div>
            <div>
              <label htmlFor="contact-source" className="mb-1.5 block text-sm font-medium text-slate-700">
                Consent source
              </label>
              <Input
                id="contact-source"
                value={source}
                onChange={(e) => setSource(e.target.value)}
                placeholder="e.g. signup form at checkout"
              />
              <p className="mt-1 text-xs text-slate-500">
                {status === "subscribed"
                  ? "Stored as consent evidence for this subscriber."
                  : "Used as the contact's source."}
              </p>
            </div>
          </div>

          {error ? (
            <p className="rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-700">{error}</p>
          ) : null}
        </div>

        <div className="flex items-center justify-end gap-2">
          <Button variant="secondary" onClick={() => close(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={() => void handleSubmit()} disabled={!email.trim() || submitting}>
            {submitting ? "Adding..." : "Add contact"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
