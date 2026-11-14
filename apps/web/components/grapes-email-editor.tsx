"use client";

import { useEffect, useRef, useState } from "react";
import type { Editor, ProjectData } from "grapesjs";
import { apiFetch } from "@/lib/auth";
import { rehostExternalImages } from "@/lib/email-assets";

export type GrapesEmailDesign = {
  project: ProjectData | null;
  html: string;
  htmlMode: "visual" | "original";
  imageWarnings: string[];
};

function shouldKeepImportedHTML(source: string): boolean {
  const parsed = new DOMParser().parseFromString(source, "text/html");
  if (/<(?:!doctype\s+html|html|head|body)\b/i.test(source)) return true;
  return Array.from(parsed.querySelectorAll("table")).some((table) => {
    const width = Number.parseInt(table.getAttribute("width") ?? table.style.width, 10);
    return Number.isFinite(width) && width >= 560;
  });
}

async function renderImportPreview(html: string, htmlMode: "original" | "visual"): Promise<string> {
  const response = await apiFetch<{ html: string }>("/templates/preview", {
    method: "POST",
    body: JSON.stringify({
      design: { version: 2, mode: "html", html_mode: htmlMode, html },
    }),
  });
  return response.html;
}

function isProjectData(value: unknown): value is ProjectData {
  return typeof value === "object" && value !== null && "pages" in value && Array.isArray(value.pages);
}

export function GrapesEmailEditor({
  project,
  html,
  htmlMode,
  imageWarnings: reportedImageWarnings,
  assetReplacements = {},
  onChange,
}: {
  project?: unknown;
  html: string;
  htmlMode: "visual" | "original";
  imageWarnings?: string[];
  assetReplacements?: Record<string, string>;
  onChange: (design: GrapesEmailDesign, changed: boolean) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Editor | null>(null);
  const initialRef = useRef({ project, html, htmlMode });
  const onChangeRef = useRef(onChange);
  const htmlSourceRef = useRef<HTMLTextAreaElement>(null);
  const updateTimerRef = useRef<number | undefined>(undefined);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [importing, setImporting] = useState(false);
  const [sourceOpen, setSourceOpen] = useState(htmlMode === "original");
  const [sourceHTML, setSourceHTML] = useState(html);
  const [findText, setFindText] = useState("");
  const [replaceText, setReplaceText] = useState("");
  const [replaceStatus, setReplaceStatus] = useState<string | null>(null);
  const [currentHTMLMode, setCurrentHTMLMode] = useState<"visual" | "original">(htmlMode);
  const currentHTMLModeRef = useRef<"visual" | "original">(htmlMode);
  const [importMode, setImportMode] = useState<"visual" | "original">(htmlMode);
  const [importModeTouched, setImportModeTouched] = useState(false);
  const [imageWarnings, setImageWarnings] = useState<string[]>([]);
  const [conversionPreview, setConversionPreview] = useState<{ before: string; after: string } | null>(null);
  const imageWarningsRef = useRef<string[]>([]);
  const suppressUpdatesRef = useRef(false);

  onChangeRef.current = onChange;

  useEffect(() => {
    setSourceHTML(html);
  }, [html]);

  useEffect(() => {
    setCurrentHTMLMode(htmlMode);
    currentHTMLModeRef.current = htmlMode;
  }, [htmlMode]);

  useEffect(() => {
    if (!reportedImageWarnings) return;
    imageWarningsRef.current = reportedImageWarnings;
    setImageWarnings(reportedImageWarnings);
  }, [reportedImageWarnings]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor || currentHTMLMode === "original" || Object.keys(assetReplacements).length === 0) return;
    suppressUpdatesRef.current = true;
    let updated = false;
    for (const component of editor.getWrapper()?.find("img") ?? []) {
      const attributes = component.getAttributes();
      const replacement = attributes.src ? assetReplacements[attributes.src] : undefined;
      if (replacement) {
        component.addAttributes({ ...attributes, src: replacement });
        updated = true;
      }
    }
    if (updated) publish(editor, false);
    suppressUpdatesRef.current = false;
  }, [assetReplacements, currentHTMLMode]);

  const publish = (instance: Editor, changed: boolean) => {
    if (currentHTMLModeRef.current === "original") return;
    const exported = instance.Commands.run("gjs-get-inlined-html");
    if (typeof exported !== "string") {
      setError("The email editor could not export the current design.");
      return;
    }
    setSourceHTML(exported);
    onChangeRef.current({
      project: instance.getProjectData(),
      html: exported,
      htmlMode: "visual",
      imageWarnings: imageWarningsRef.current,
    }, changed);
  };

  useEffect(() => {
    let active = true;
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
        } else if (initialRef.current.html.trim() && initialRef.current.htmlMode !== "original") {
          editor.setComponents(initialRef.current.html);
        }

        editorRef.current = editor;
        if (initialRef.current.htmlMode !== "original") publish(editor, false);
        editor.on("update", () => {
          if (suppressUpdatesRef.current || currentHTMLModeRef.current === "original") return;
          if (updateTimerRef.current !== undefined) window.clearTimeout(updateTimerRef.current);
          updateTimerRef.current = window.setTimeout(() => {
            updateTimerRef.current = undefined;
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
      if (updateTimerRef.current !== undefined) window.clearTimeout(updateTimerRef.current);
      updateTimerRef.current = undefined;
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
    if (!editor || importing) return;
    setImporting(true);
    setError(null);
    setConversionPreview(null);
    try {
      const imported = await rehostExternalImages(await file.text());
      const selectedMode = !importModeTouched && shouldKeepImportedHTML(imported.html) ? "original" : importMode;
      setImportMode(selectedMode);
      if (selectedMode === "visual" && !window.confirm("Convert to editable blocks? GrapesJS may change table-based email layouts. A before-and-after preview will be shown.")) {
        return;
      }
      if (updateTimerRef.current !== undefined) window.clearTimeout(updateTimerRef.current);
      updateTimerRef.current = undefined;
      imageWarningsRef.current = imported.warnings;
      setImageWarnings(imported.warnings);
      setSourceHTML(imported.html);
      currentHTMLModeRef.current = selectedMode;
      setCurrentHTMLMode(selectedMode);
      if (selectedMode === "original") {
        setSourceOpen(true);
        onChangeRef.current({
          project: null,
          html: imported.html,
          htmlMode: "original",
          imageWarnings: imported.warnings,
        }, true);
      } else {
        const before = await renderImportPreview(imported.html, "original");
        suppressUpdatesRef.current = true;
        editor.setComponents(imported.html);
        suppressUpdatesRef.current = false;
        const converted = editor.Commands.run("gjs-get-inlined-html");
        if (typeof converted !== "string") throw new Error("The converted email could not be previewed.");
        const after = await renderImportPreview(converted, "visual");
        setConversionPreview({ before, after });
        publish(editor, true);
      }
    } catch (importError) {
      suppressUpdatesRef.current = false;
      setError(importError instanceof Error ? importError.message : "The HTML file could not be imported.");
    } finally {
      setImporting(false);
    }
  };

  const applyHTMLSource = () => {
    const editor = editorRef.current;
    if (!editor) return;
    try {
      if (currentHTMLMode === "original") {
        onChangeRef.current({
          project: null,
          html: sourceHTML,
          htmlMode: "original",
          imageWarnings,
        }, true);
        return;
      }
      editor.setComponents(sourceHTML);
      publish(editor, true);
    } catch (sourceError) {
      setError(sourceError instanceof Error ? sourceError.message : "The HTML could not be applied.");
    }
  };

  const clearDesign = () => {
    const editor = editorRef.current;
    if (!editor) return;
    if (currentHTMLMode === "original") {
      setSourceHTML("");
      imageWarningsRef.current = [];
      onChangeRef.current({
        project: null,
        html: "",
        htmlMode: "original",
        imageWarnings: [],
      }, true);
      setImageWarnings([]);
      return;
    }
    editor.setComponents("");
    publish(editor, true);
  };

  const keepSourceAsOriginalHTML = () => {
    if (updateTimerRef.current !== undefined) window.clearTimeout(updateTimerRef.current);
    updateTimerRef.current = undefined;
    currentHTMLModeRef.current = "original";
    setCurrentHTMLMode("original");
    setSourceOpen(true);
    onChangeRef.current({
      project: null,
      html: sourceHTML,
      htmlMode: "original",
      imageWarnings,
    }, true);
  };

  const updateOriginalHTML = (value: string) => {
    setSourceHTML(value);
    onChangeRef.current({
      project: null,
      html: value,
      htmlMode: "original",
      imageWarnings,
    }, true);
  };

  const replaceOriginalText = () => {
    if (!findText) {
      setReplaceStatus("Enter the exact text, URL, or color to find.");
      return;
    }
    const matches = sourceHTML.split(findText).length - 1;
    if (matches === 0) {
      setReplaceStatus("No exact matches were found.");
      return;
    }
    updateOriginalHTML(sourceHTML.split(findText).join(replaceText));
    setReplaceStatus(`Replaced ${matches} occurrence${matches === 1 ? "" : "s"}.`);
  };

  const copyHTMLSource = async () => {
    try {
      if (!navigator.clipboard) throw new Error("Clipboard access is unavailable in this browser.");
      await navigator.clipboard.writeText(sourceHTML);
    } catch (copyError) {
      setError(copyError instanceof Error ? copyError.message : "The HTML could not be copied.");
    }
  };

  return (
    <section className="space-y-3" aria-label="Email design editor">
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-slate-200 bg-white p-3">
        <label className="flex items-center gap-2 text-sm text-slate-700">
          Import mode
          <select
            aria-label="HTML import mode"
            className="rounded-md border border-slate-200 bg-white px-2 py-2 text-sm"
            value={importMode}
            onChange={(event) => {
              setImportMode(event.target.value as "visual" | "original");
              setImportModeTouched(true);
            }}
          >
            <option value="visual">Visual editing</option>
            <option value="original">Keep original HTML</option>
          </select>
        </label>
        <label
          aria-disabled={!ready || importing}
          className={`inline-flex items-center rounded-md border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 ${
            ready && !importing ? "cursor-pointer hover:bg-slate-50" : "cursor-not-allowed opacity-50"
          }`}
        >
          {importing ? "Importing HTML…" : "Import HTML"}
          <input
            aria-label="Import HTML"
            data-testid="email-html-import-input"
            className="sr-only"
            type="file"
            accept=".html,.htm,text/html"
            disabled={!ready || importing}
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
            accept="image/png,image/jpeg,image/gif,image/webp"
            disabled={!ready || uploading}
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              if (file) void uploadImage(file);
              event.currentTarget.value = "";
            }}
          />
        </label>
        <span className="text-xs text-slate-500">
          {currentHTMLMode === "original"
            ? "Original HTML is preserved; visual editing is limited in this mode."
            : "Imported HTML is parsed into editable blocks. Imported images are stored by Sendwise."}
        </span>
        <button
          type="button"
          className="rounded-md border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={!ready}
          aria-expanded={sourceOpen}
          onClick={() => setSourceOpen((open) => !open)}
        >
          {sourceOpen ? "Hide HTML" : "Edit HTML"}
        </button>
        <button
          type="button"
          className="rounded-md border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={!ready}
          onClick={clearDesign}
        >
          Clear design
        </button>
      </div>
      {sourceOpen ? (
        <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-3">
          <label htmlFor="email-html-source" className="block text-sm font-medium text-slate-800">
            HTML source
          </label>
          <textarea
            ref={htmlSourceRef}
            id="email-html-source"
            aria-label="HTML source"
            value={sourceHTML}
            onChange={(event) => currentHTMLMode === "original"
              ? updateOriginalHTML(event.target.value)
              : setSourceHTML(event.target.value)}
            spellCheck={false}
            className="min-h-48 w-full rounded-md border border-slate-200 bg-white p-3 font-mono text-xs leading-5 text-slate-900 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
          <p className="text-xs text-slate-500">Paste or edit HTML here. Use Select all, then copy or replace the source.</p>
          {currentHTMLMode === "original" ? (
            <div className="grid gap-2 rounded-md bg-slate-50 p-3 sm:grid-cols-[1fr_1fr_auto]">
              <label className="text-xs font-medium text-slate-700">
                Find exact text, link, image URL, or color
                <input
                  aria-label="Find in original HTML"
                  className="mt-1 w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm"
                  value={findText}
                  onChange={(event) => setFindText(event.target.value)}
                />
              </label>
              <label className="text-xs font-medium text-slate-700">
                Replace with
                <input
                  aria-label="Replace in original HTML"
                  className="mt-1 w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm"
                  value={replaceText}
                  onChange={(event) => setReplaceText(event.target.value)}
                />
              </label>
              <button type="button" className="self-end rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 hover:bg-slate-50" onClick={replaceOriginalText}>
                Replace all
              </button>
              {replaceStatus ? <p role="status" className="text-xs text-slate-600 sm:col-span-3">{replaceStatus}</p> : null}
            </div>
          ) : null}
          <div className="flex flex-wrap gap-2">
            <button type="button" className="rounded-md border border-slate-200 px-3 py-2 text-sm text-slate-700 hover:bg-slate-50" onClick={() => htmlSourceRef.current?.select()}>
              Select all
            </button>
            <button type="button" className="rounded-md border border-slate-200 px-3 py-2 text-sm text-slate-700 hover:bg-slate-50" onClick={() => void copyHTMLSource()}>
              Copy HTML
            </button>
            <button type="button" className="rounded-md border border-slate-200 px-3 py-2 text-sm text-slate-700 hover:bg-slate-50" onClick={() => setSourceHTML("")}>
              Clear source
            </button>
            <button type="button" className="rounded-md bg-indigo-600 px-3 py-2 text-sm font-medium text-white hover:bg-indigo-500" onClick={applyHTMLSource}>
              Apply HTML
            </button>
            {currentHTMLMode !== "original" ? (
              <button type="button" className="rounded-md border border-slate-200 px-3 py-2 text-sm text-slate-700 hover:bg-slate-50" onClick={keepSourceAsOriginalHTML}>
                Keep source as original HTML
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
      {error ? <p role="alert" className="rounded-lg bg-rose-50 p-3 text-sm text-rose-800">{error}</p> : null}
      {!ready && !error ? <p role="status" className="text-sm text-slate-500">Loading email editor…</p> : null}
      {conversionPreview ? (
        <section className="space-y-3 rounded-lg border border-amber-200 bg-amber-50 p-4" aria-label="HTML conversion comparison">
          <div>
            <h2 className="text-sm font-semibold text-amber-950">Converted layout comparison</h2>
            <p className="text-xs text-amber-900">GrapesJS conversion can alter email tables and spacing. Compare the sanitized output before using the converted version.</p>
          </div>
          <div className="grid gap-3 lg:grid-cols-2">
            <div>
              <h3 className="mb-1 text-xs font-medium text-slate-700">Original HTML</h3>
              <iframe title="Original HTML preview" sandbox="" srcDoc={conversionPreview.before} className="h-96 w-full rounded border bg-white" />
            </div>
            <div>
              <h3 className="mb-1 text-xs font-medium text-slate-700">Converted editable blocks</h3>
              <iframe title="Converted HTML preview" sandbox="" srcDoc={conversionPreview.after} className="h-96 w-full rounded border bg-white" />
            </div>
          </div>
        </section>
      ) : null}
      {currentHTMLMode === "original" ? (
        <p className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900">
          Keep original HTML mode preserves the imported table structure. Edit the source above; GrapesJS visual block editing is disabled.
        </p>
      ) : null}
      <div
        ref={containerRef}
        data-testid="grapes-email-editor"
        className={`${currentHTMLMode === "original" ? "hidden" : "min-h-[640px] overflow-hidden rounded-lg border border-slate-200 bg-white"}`}
      />
    </section>
  );
}
