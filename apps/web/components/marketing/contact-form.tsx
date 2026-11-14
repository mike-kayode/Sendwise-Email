"use client";

import { useState } from "react";
import { apiUrl } from "@/lib/api-url";

const topics = [
  "Plans and pricing",
  "Migrating my sending",
  "Domain verification help",
  "Transactional email and API",
  "Report abuse or safety concern",
  "Something else",
];

export function ContactForm() {
  const [state, setState] = useState<"editing" | "sending" | "sent" | "error">("editing");
  const [error, setError] = useState("");

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    setState("sending");
    setError("");
    const values = new FormData(form);
    try {
      const response = await fetch(apiUrl("/api/v1/public/contact"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: values.get("name"),
          email: values.get("email"),
          topic: values.get("topic"),
          message: values.get("message"),
          website: values.get("website"),
        }),
      });
      if (!response.ok) {
        const body = await response.json().catch(() => null) as { error?: { message?: string }; message?: string } | null;
        throw new Error(body?.error?.message ?? body?.message ?? "We could not send your message. Please try again.");
      }
      form.reset();
      setState("sent");
    } catch (submitError) {
      setState("error");
      setError(submitError instanceof Error ? submitError.message : "We could not send your message. Please try again.");
    }
  }

  if (state === "sent") {
    return (
      <div className="card contact-form-card contact-success" role="status">
        <div className="ico contact-success-icon" aria-hidden="true">✓</div>
        <h2 className="h3">Message sent</h2>
        <p className="small">Your message has been recorded. Thank you for reaching out.</p>
        <button type="button" className="btn btn-ghost-light" onClick={() => setState("editing")}>Send another message</button>
      </div>
    );
  }

  return (
    <form className="card contact-form-card form-stack" onSubmit={(event) => void submit(event)}>
      <div>
        <label className="label" htmlFor="contact-name">Full name</label>
        <input className="field" id="contact-name" name="name" type="text" autoComplete="name" minLength={2} maxLength={120} required disabled={state === "sending"} />
      </div>
      <div>
        <label className="label" htmlFor="contact-email">Work email</label>
        <input className="field" id="contact-email" name="email" type="email" autoComplete="email" maxLength={254} required disabled={state === "sending"} />
      </div>
      <div>
        <label className="label" htmlFor="contact-topic">What is this about?</label>
        <select className="field" id="contact-topic" name="topic" required disabled={state === "sending"}>
          {topics.map((topic) => <option key={topic}>{topic}</option>)}
        </select>
      </div>
      <div>
        <label className="label" htmlFor="contact-message">Message</label>
        <textarea className="field" id="contact-message" name="message" minLength={20} maxLength={4000} required disabled={state === "sending"} />
      </div>
      <div className="contact-honeypot" aria-hidden="true">
        <label htmlFor="contact-website">Leave this field empty</label>
        <input id="contact-website" name="website" type="text" tabIndex={-1} autoComplete="off" />
      </div>
      {state === "error" ? <p className="form-error" role="alert">{error}</p> : null}
      <button type="submit" className="btn btn-ink contact-submit" disabled={state === "sending"}>
        {state === "sending" ? "Sending..." : "Send message"}
      </button>
      <p className="small">Do not include passwords, API keys, or other sensitive account credentials.</p>
    </form>
  );
}
