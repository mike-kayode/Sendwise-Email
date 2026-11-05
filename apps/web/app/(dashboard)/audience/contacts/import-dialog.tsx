"use client";

import { useState, useRef, useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Upload,
  FileText,
  Loader2,
  CheckCircle,
  AlertCircle,
  Trash2,
  ChevronRight,
  ChevronLeft,
} from "lucide-react";
import { useToast } from "@/components/ui/use-toast";
import { apiFetch } from "@/lib/auth";
import type { ContactImport } from "@/lib/auth";

type ImportStep = "upload" | "map" | "preview" | "importing" | "complete";

type ImportResult = {
  id: string;
  status: string;
  total_rows: number;
  processed_rows: number;
  imported: number;
  skipped: number;
  invalid: number;
  last_error?: string;
  errors: Array<{ row: number; message: string }>;
};

type ContactField =
  | "email"
  | "first_name"
  | "last_name"
  | "phone"
  | "company"
  | "title"
  | "none";

export function ImportDialog({
  open,
  onOpenChange,
  onImportComplete,
}: {
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onImportComplete?: () => void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const [step, setStep] = useState<ImportStep>("upload");
  const toast = useToast();

  // Sync controlled open state with internal state
  useEffect(() => {
    setIsOpen(!!open);
  }, [open]);

  const handleOpenChange = (next: boolean) => {
    setIsOpen(next);
    onOpenChange?.(next);
    if (!next) {
      // Reset when closed
      setStep("upload");
      setFile(null);
      setFileError(null);
      setHeaders([]);
      setPreviewRows([]);
      setMapping({});
      setJobId(null);
      setImportResult(null);
      setIsImporting(false);
      stopPolling();
    }
  };

  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const [headers, setHeaders] = useState<string[]>([]);
  const [previewRows, setPreviewRows] = useState<string[][]>([]);
  const [mapping, setMapping] = useState<Record<string, ContactField>>({});
  const [jobId, setJobId] = useState<string | null>(null);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [isImporting, setIsImporting] = useState(false);
  const pollInterval = useRef<ReturnType<typeof setInterval> | null>(null);

  const CONTACT_FIELDS: { value: ContactField; label: string }[] = [
    { value: "email", label: "Email" },
    { value: "first_name", label: "First Name" },
    { value: "last_name", label: "Last Name" },
    { value: "phone", label: "Phone" },
    { value: "company", label: "Company" },
    { value: "title", label: "Job Title" },
    { value: "none", label: "Skip" },
  ];

  const handleFileSelect = (selectedFile: File) => {
    if (!selectedFile.name.toLowerCase().endsWith(".csv")) {
      setFileError("Please upload a CSV file (.csv extension).");
      return;
    }
    if (selectedFile.size > 16 * 1024 * 1024) {
      setFileError("File too large. Max 16 MB.");
      return;
    }
    setFile(selectedFile);
    setFileError(null);
    const reader = new FileReader();
    reader.onload = (e) => {
      const text = e.target?.result as string;
      const lines = text.split("\n").filter((l) => l.trim());
      if (lines.length === 0) {
        setFileError("Empty file.");
        return;
      }
      const hdrs = lines[0].split(",").map((h) => h.trim().replace(/^"|"$/g, ""));
      setHeaders(hdrs);
      setPreviewRows(lines.slice(1, 6).map((l) => l.split(",").map((c) => c.trim())));
      const auto: Record<string, ContactField> = {};
      hdrs.forEach((h) => {
        const l = h.toLowerCase().trim();
        if (l === "email" || l === "e-mail") auto[h] = "email";
        else if (l.includes("first")) auto[h] = "first_name";
        else if (l.includes("last")) auto[h] = "last_name";
        else if (l === "phone" || l === "mobile") auto[h] = "phone";
        else if (l === "company" || l === "org") auto[h] = "company";
        else if (l === "title") auto[h] = "title";
        else auto[h] = "none";
      });
      setMapping(auto);
    };
    reader.readAsText(selectedFile);
  };

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
  };

  const handleFileDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    if (e.dataTransfer.files.length > 0) {
      handleFileSelect(e.dataTransfer.files[0]);
    }
  };

  const mapField = (header: string, field: ContactField) => {
    setMapping((prev) => ({ ...prev, [header]: field }));
  };

  const isFieldMapped = (field: ContactField) => {
    return Object.values(mapping).includes(field);
  };

  const getMappedHeader = (field: ContactField) => {
    return Object.entries(mapping).find(([, v]) => v === field)?.[0];
  };

  const handleImport = async () => {
    if (!isFieldMapped("email")) {
      toast.toast({
        title: "Missing email",
        description: "Map a column to Email.",
        variant: "destructive",
      });
      return;
    }
    if (!file) return;

    const formData = new FormData();
    formData.append("file", file);
    formData.append("mapping", JSON.stringify(mapping));

    setIsImporting(true);
    try {
      const result = await apiFetch<{ id: string }>("/contacts/import", {
        method: "POST",
        body: formData,
      });
      setJobId(result.id);
      setStep("importing");
      startPolling(result.id);
    } catch (err) {
      setIsImporting(false);
      toast.toast({
        title: "Import failed",
        description: err instanceof Error ? err.message : "Unknown error",
        variant: "destructive",
      });
    }
  };

  const startPolling = (id: string) => {
    stopPolling();
    let failures = 0;
    const startedAt = Date.now();
    pollInterval.current = setInterval(async () => {
      try {
        const result = await apiFetch<ImportResult>(`/contact-imports/${id}`);
        failures = 0;
        setImportResult(result);
        const pending = ["queued", "pending", "processing", "running"].includes(result.status);
        if (!pending) {
          stopPolling();
          setIsImporting(false);
          setStep("complete");
          const ok = result.status === "completed";
          toast.toast({
            title: ok ? "Import complete" : "Import finished with errors",
            description: `Imported ${result.imported} contacts (${result.skipped} skipped, ${result.invalid} invalid).`,
            variant: ok ? "default" : "destructive",
          });
          onImportComplete?.();
        } else if (Date.now() - startedAt > 5 * 60 * 1000) {
          stopPolling();
          setIsImporting(false);
          toast.toast({
            title: "Import is taking too long",
            description: "It is still queued on the server. Check back shortly.",
            variant: "destructive",
          });
        }
      } catch (err) {
        console.error("Polling error:", err);
        if (++failures >= 5) {
          stopPolling();
          setIsImporting(false);
          toast.toast({
            title: "Lost contact with the server",
            description: err instanceof Error ? err.message : "Unknown error",
            variant: "destructive",
          });
        }
      }
    }, 2000);
  };

  const stopPolling = () => {
    if (pollInterval.current) {
      clearInterval(pollInterval.current);
      pollInterval.current = null;
    }
  };

  useEffect(() => {
    return () => {
      stopPolling();
    };
  }, []);

  const downloadErrors = () => {
    if (!importResult?.errors?.length) return;
    const csvContent = [
      ["row", "message"],
      ...importResult.errors.map((e) => [e.row, e.message]),
    ]
      .map((row) => row.map((c) => `"${String(c).replace(/"/g, '""')}"`).join(","))
      .join("\n");
    const blob = new Blob([csvContent], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `import-errors-${jobId}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const restart = () => {
    setStep("upload");
    setFile(null);
    setFileError(null);
    setHeaders([]);
    setPreviewRows([]);
    setMapping({});
    setJobId(null);
    setImportResult(null);
    setIsImporting(false);
    stopPolling();
  };

  const stepLabels: Record<ImportStep, string> = {
    upload: "Upload CSV",
    map: "Map Columns",
    preview: "Preview",
    importing: "Importing",
    complete: "Complete",
  };

  const stepOrder: ImportStep[] = ["upload", "map", "preview", "importing", "complete"];

  return (
    <Dialog open={isOpen} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" disabled={isImporting}>
          <Upload className="h-4 w-4 mr-2" /> Import Contacts
        </Button>
      </DialogTrigger>
      <DialogContent>
        <div className="flex flex-col space-y-1 p-4 border-b border-slate-100">
          <DialogTitle>Import Contacts</DialogTitle>
          <DialogDescription>
            Upload a CSV file to import contacts in bulk.
          </DialogDescription>
        </div>
        <DialogContent className="p-0">
          <div className="flex items-center gap-2 mb-4 px-4">
            {stepOrder.map((stepKey, idx) => {
              const isCompleted = stepOrder.indexOf(step) > stepOrder.indexOf(stepKey);
              const isActive = step === stepKey;
              return (
                <div key={stepKey} className="flex items-center gap-2">
                  {isCompleted ? (
                    <CheckCircle className="h-4 w-4 text-emerald-600" />
                  ) : isActive ? (
                    <Loader2 className="h-4 w-4 animate-spin text-indigo-600" />
                  ) : (
                    <div className="h-4 w-4 rounded-full border border-slate-300" />
                  )}
                  {stepOrder.indexOf(stepKey) < stepOrder.length - 1 && (
                    <ChevronRight className="h-3 w-3 text-slate-400" />
                  )}
                  <span className={`text-xs ${isActive ? "font-medium text-indigo-600" : "text-slate-500"}`}>
                    {stepLabels[stepKey]}
                  </span>
                </div>
              );
            })}
          </div>
          {step === "upload" && (
            <div className="p-6">
              <div
                className="border-2 border-dashed border-slate-300 rounded-lg p-8 text-center hover:border-indigo-500 hover:bg-slate-50 cursor-pointer"
                onDragOver={handleDragOver}
                onDrop={handleFileDrop}
              >
                <Upload className="h-10 w-10 mx-auto text-slate-400 mb-3" />
                <p className="text-sm font-medium text-slate-900 mb-1">
                  Drop CSV file here
                </p>
                <p className="text-xs text-slate-500 mb-4">or click to browse</p>
                <Button variant="outline" onClick={() => document.getElementById("file-input")?.click()}>
                  Choose CSV file
                  <input
                    id="file-input"
                    type="file"
                    accept=".csv"
                    className="hidden"
                    onChange={(e) => {
                      if (e.target.files?.[0]) handleFileSelect(e.target.files[0]);
                    }}
                  />
                </Button>
                {fileError && (
                  <div className="mt-4 text-sm text-rose-600">{fileError}</div>
                )}
                {file && (
                  <div className="mt-4 flex items-center gap-2 px-4 py-2 rounded-lg bg-slate-50 border border-slate-200">
                    <FileText className="h-4 w-4 text-slate-500" />
                    <div className="flex-1">
                      <p className="text-sm font-medium text-slate-900">{file.name}</p>
                      <p className="text-xs text-slate-500">
                        {(file.size / 1024 / 1024).toFixed(2)} MB
                      </p>
                    </div>
                    <Button variant="danger" size="sm" onClick={() => {
                      setFile(null);
                      setHeaders([]);
                      setPreviewRows([]);
                      setMapping({});
                    }}>
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  </div>
                )}
              </div>
            </div>
          )}
          {step === "map" && (
            <div className="p-6">
              <div className="mb-6">
                <h3 className="text-sm font-semibold text-slate-900 mb-3">
                  Map columns to contact fields
                </h3>
                <div className="grid gap-3">
                  {headers.map((header) => (
                    <div key={header} className="flex items-center justify-between p-3 rounded-lg border border-slate-200">
                      <div className="flex items-center gap-2">
                        <span className="text-sm text-slate-900">{header}</span>
                      </div>
                      <Select value={mapping[header] || "none"} onValueChange={(val) => mapField(header, val as ContactField)}>
                        <SelectTrigger className="w-full min-w-[200px]">
                          <SelectValue placeholder="Select field" />
                        </SelectTrigger>
                        <SelectContent>
                          {CONTACT_FIELDS.map((field) => (
                            <SelectItem key={field.value} value={field.value}>
                              {field.label}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  ))}
                </div>
                {!isFieldMapped("email") && (
                  <div className="mt-3 p-3 rounded-lg border border-rose-200 bg-rose-50">
                    <AlertCircle className="h-4 w-4 text-rose-600 mr-2" />
                    <span className="text-sm text-rose-700">
                      Email is required. Please map an email column.
                    </span>
                  </div>
                )}
              </div>
            </div>
          )}
          {step === "preview" && (
            <div className="p-6">
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-sm font-semibold text-slate-900">Preview</h3>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" onClick={() => setStep("map" as const)}>
                    <ChevronLeft className="h-3 w-3 mr-1" /> Back
                  </Button>
                  <Button onClick={handleImport} className="bg-indigo-600 hover:bg-indigo-700">
                    Start Import <ChevronRight className="h-3 w-3 ml-1" />
                  </Button>
                </div>
              </div>
              <div className="rounded-lg border border-slate-200 overflow-hidden">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm text-left">
                    <thead className="bg-slate-50">
                      <tr>
                        {headers.map((h, i) => (
                          <th key={i} className="px-3 py-2 text-xs font-semibold text-slate-500 uppercase border-b border-slate-200">
                            {h}
                          </th>
                        ))}
                        <th className="px-3 py-2 text-xs font-semibold text-slate-500 uppercase border-b border-slate-200">Status</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-slate-200">
                      {previewRows.map((row, i) => (
                        <tr key={i}>
                          {row.map((cell, j) => (
                            <td key={j} className="px-3 py-2 text-xs text-slate-600 border-b border-slate-100">{cell}</td>
                          ))}
                          <td className="px-3 py-2 text-xs text-slate-400 border-b border-slate-100">✓</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          )}
          {step === "importing" && (
            <div className="p-6 flex flex-col items-center justify-center min-h-[300px]">
              <Loader2 className="h-8 w-8 animate-spin text-indigo-600 mb-4" />
              <p className="text-sm text-slate-600">Processing your import...</p>
              <p className="text-xs text-slate-400 mt-2">
                This may take a moment for large files.
              </p>
            </div>
          )}
          {step === "complete" && importResult && (
            <div className="p-6">
              <div className="flex items-center justify-center mb-6">
                <div className="w-12 h-12 rounded-full bg-emerald-100 flex items-center justify-center">
                  <CheckCircle className="h-6 w-6 text-emerald-600" />
                </div>
              </div>
              <h3 className="text-lg font-semibold text-slate-900 mb-2">
                Import {importResult.status === "completed" ? "Complete" : "Completed"}
              </h3>
              <div className="grid gap-4">
                <div className="rounded-lg border border-slate-200 p-4">
                  <p className="text-xs text-slate-500 uppercase">Total rows</p>
                  <p className="text-sm font-semibold text-slate-900">{importResult.total_rows}</p>
                </div>
                <div className="rounded-lg border border-slate-200 p-4">
                  <p className="text-xs text-slate-500 uppercase">Imported</p>
                  <p className="text-sm font-semibold text-emerald-600">{importResult.imported}</p>
                </div>
                <div className="rounded-lg border border-slate-200 p-4">
                  <p className="text-xs text-slate-500 uppercase">Skipped</p>
                  <p className="text-sm font-semibold text-amber-600">{importResult.skipped}</p>
                </div>
                <div className="rounded-lg border border-slate-200 p-4">
                  <p className="text-xs text-slate-500 uppercase">Invalid</p>
                  <p className="text-sm font-semibold text-rose-600">{importResult.invalid}</p>
                </div>
              </div>
              {importResult.errors && importResult.errors.length > 0 && (
                <div className="mt-6 rounded-lg border border-slate-200 p-4">
                  <h4 className="text-xs font-semibold text-slate-500 uppercase mb-3">
                    Errors ({importResult.errors.length})
                  </h4>
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm text-left">
                      <thead>
                        <tr>
                          <th className="px-3 py-1 text-xs font-semibold text-slate-500 border-b border-slate-200">
                            Row
                          </th>
                          <th className="px-3 py-1 text-xs font-semibold text-slate-500 border-b border-slate-200">
                            Error
                          </th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-slate-200">
                        {importResult.errors.slice(0, 10).map((err, i) => (
                          <tr key={i}>
                            <td className="px-3 py-2 text-xs text-slate-600 border-b border-slate-100">
                              {err.row}
                            </td>
                            <td className="px-3 py-2 text-xs text-slate-600 border-b border-slate-100">
                              {err.message}
                            </td>
                          </tr>
                        ))}
                        {importResult.errors.length > 10 && (
                          <tr>
                            <td className="px-3 py-2 text-xs text-slate-500 border-b border-slate-100" colSpan={2}>
                              ...and {importResult.errors.length - 10} more errors. Download the error report for details.
                            </td>
                          </tr>
                        )}
                      </tbody>
                    </table>
                  </div>
                  <div className="mt-4 flex justify-end">
                    <Button variant="outline" size="sm" onClick={downloadErrors}>
                      <FileText className="h-3 w-3 mr-1" /> Download error report
                    </Button>
                  </div>
                </div>
              )}
              <div className="mt-6 flex justify-end">
                <Button variant="outline" onClick={restart}>
                  Import another file
                </Button>
                <Button onClick={() => {
                  setIsOpen(false);
                  onImportComplete?.();
                }}>
                  Close
                </Button>
              </div>
            </div>
          )}
        </DialogContent>
        <div className="flex justify-between w-full p-4 border-t border-slate-100 bg-slate-50">
          {step !== "complete" && (
            <div className="flex justify-between w-full">
              {step !== "upload" && (
                <Button variant="outline" onClick={() => setStep("upload" as const)}>
                  Back
                </Button>
              )}
              {step === "preview" && (
                <Button onClick={handleImport} className="bg-indigo-600 hover:bg-indigo-700">
                  Start Import
                </Button>
              )}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}