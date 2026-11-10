"use client";

import { useEffect, useRef, useState } from "react";
import type { Editor, ProjectData } from "grapesjs";
import { apiFetch } from "@/lib/auth";

export type GrapesEmailDesign = {
  project: ProjectData;
  html: string;
};

function isProjectData(value: unknown): value is ProjectData {
  return typeof value === "object" && value !== null && "pages" in value && Array.isArray(value.pages);
}

export function GrapesEmailEditor({
  project,
  html,
  onChange,
}: {
  project?: unknown;
  html: string;
  onChange: (design: GrapesEmailDesign, changed: boolean) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Editor | null>(null);
  const initialRef = useRef({ project, html });
  const onChangeRef = useRef(onChange);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);

  onChangeRef.current = onChange;

  const publish = (instance: Editor, changed: boolean) => {
    const exported = instance.Commands.run("gjs-get-inlined-html");
    if (typeof exported !== "string") {
      setError("The email editor could not export the current design.");
      return;
    }
    onChangeRef.current({
      project: instance.getProjectData(),
      html: exported,
    }, changed);
  };

  useEffect(() => {
    let active = true;
    let updateTimer: number | undefined;
    let editor: Editor | undefined;

    const initialize = async () => {
      try {
        const [{ default: grapesjs }, { default: newsletterPreset }] = await Promise.all([
          import("grapesjs"),
          import("grapesjs-preset-newsletter"),
        ]);
        if (!active || !containerRef.current) return;

        editor = grapesjs.init({
          container: containerRef.current,
          height: "640px",
          width: "auto",
          fromElement: false,
          storageManager: false,
          assetManager: { upload: false },
          plugins: [
            (instance) => newsletterPreset(instance, {
              showStylesOnChange: true,
              showBlocksOnLoad: true,
            }),
          ],
        });

        if (isProjectData(initialRef.current.project)) {
          editor.loadProjectData(initialRef.current.project);
        } else if (initialRef.current.html.trim()) {
          editor.setComponents(initialRef.current.html);
        }

        editorRef.current = editor;
        publish(editor, false);
        editor.on("update", () => {
          if (updateTimer !== undefined) window.clearTimeout(updateTimer);
          updateTimer = window.setTimeout(() => {
            if (!editor) return;
            publish(editor, true);
          }, 300);
        });
        setReady(true);
      } catch (initError) {
        setError(initError instanceof Error ? initError.message : "The email editor could not be initialized.");
      }
    };

    void initialize();

    return () => {
      active = false;
      if (updateTimer !== undefined) window.clearTimeout(updateTimer);
      editor?.destroy();
      editorRef.current = null;
    };
  }, []);

  const uploadImage = async (file: File) => {
    const editor = editorRef.current;
    if (!editor || uploading) return;
    setUploading(true);
    setError(null);
    try {
      const body = new FormData();
      body.append("file", file);
      const asset = await apiFetch<{ url: string }>("/assets", { method: "POST", body });
      editor.AssetManager.add({ src: asset.url, name: file.name });
      editor.AssetManager.open();
    } catch (uploadError) {
      setError(uploadError instanceof Error ? uploadError.message : "The image could not be uploaded.");
    } finally {
      setUploading(false);
    }
  };

  const importHTML = async (file: File) => {
    const editor = editorRef.current;
    if (!editor) return;
    setError(null);
    try {
      editor.setComponents(await file.text());
      publish(editor, true);
    } catch (importError) {
      setError(importError instanceof Error ? importError.message : "The HTML file could not be imported.");
    }
  };

  return (
    <section className="space-y-3" aria-label="Email design editor">
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-slate-200 bg-white p-3">
        <label
          aria-disabled={!ready}
          className={`inline-flex items-center rounded-md border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 ${
            ready ? "cursor-pointer hover:bg-slate-50" : "cursor-not-allowed opacity-50"
          }`}
        >
          Import HTML
          <input
            aria-label="Import HTML"
            data-testid="email-html-import-input"
            className="sr-only"
            type="file"
            accept=".html,.htm,text/html"
            disabled={!ready}
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              if (file) void importHTML(file);
              event.currentTarget.value = "";
            }}
          />
        </label>
        <label
          aria-disabled={!ready || uploading}
          className={`inline-flex items-center rounded-md border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 ${
            ready && !uploading ? "cursor-pointer hover:bg-slate-50" : "cursor-not-allowed opacity-50"
          }`}
        >
          {uploading ? "Uploading image…" : "Upload image"}
          <input
            aria-label="Upload image"
            data-testid="email-image-upload-input"
            className="sr-only"
            type="file"
            accept="image/png,image/jpeg,image/gif"
            disabled={!ready || uploading}
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              if (file) void uploadImage(file);
              event.currentTarget.value = "";
            }}
          />
        </label>
        <span className="text-xs text-slate-500">
          Imported HTML is parsed into editable blocks. Uploaded images are stored by Sendwise.
        </span>
      </div>
      {error ? <p role="alert" className="rounded-lg bg-rose-50 p-3 text-sm text-rose-800">{error}</p> : null}
      {!ready && !error ? <p role="status" className="text-sm text-slate-500">Loading email editor…</p> : null}
      <div
        ref={containerRef}
        data-testid="grapes-email-editor"
        className="min-h-[640px] overflow-hidden rounded-lg border border-slate-200 bg-white"
      />
    </section>
  );
}
