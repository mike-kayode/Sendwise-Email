export type CSVContactField =
  | "email"
  | "first_name"
  | "last_name"
  | "phone"
  | "company"
  | "title"
  | "city"
  | "country"
  | "custom"
  | "none";

export type CSVColumnMapping = {
  email: string;
  first_name?: string;
  last_name?: string;
  custom_fields?: Record<string, string>;
  has_header: boolean;
};

export type CSVPreview = {
  headers: string[];
  rows: string[][];
  hasHeader: boolean;
};

export async function readCSVFile(file: File): Promise<string> {
  let bytes = new Uint8Array(await file.arrayBuffer());
  if (bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) {
    bytes = bytes.subarray(3);
  }

  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return new TextDecoder("windows-1252", { fatal: true }).decode(bytes);
  }
}

export function parseCSVPreview(source: string, previewLimit = 5): CSVPreview {
  const text = source.replace(/^\uFEFF/, "");
  const commaHeader = parseRecords(text, ",", 1)[0] ?? [];
  const semicolonHeader = parseRecords(text, ";", 1)[0] ?? [];
  const delimiter = semicolonHeader.length > commaHeader.length ? ";" : ",";
  const records = parseRecords(text, delimiter, previewLimit + 1);
  const firstRecord = records[0] ?? [];
  if (firstRecord.length === 0 || firstRecord.every((value) => !value.trim())) {
    throw new Error("The CSV file is empty.");
  }

  const hasHeader = firstRecord.some((value) =>
    ["email", "email_address", "e_mail", "mail"].includes(normalizeHeader(value)),
  ) || !firstRecord.some(looksLikeEmail);

  return {
    headers: hasHeader
      ? firstRecord.map((value) => value.trim())
      : firstRecord.map((_, index) => `Column ${index + 1}`),
    rows: (hasHeader ? records.slice(1) : records).slice(0, previewLimit),
    hasHeader,
  };
}

export function autoMapCSVHeader(header: string): CSVContactField {
  switch (normalizeHeader(header)) {
    case "email":
    case "email_address":
    case "e_mail":
    case "mail":
      return "email";
    case "first":
    case "first_name":
    case "firstname":
    case "given":
    case "given_name":
      return "first_name";
    case "last":
    case "last_name":
    case "lastname":
    case "family_name":
    case "surname":
      return "last_name";
    case "phone":
    case "phone_number":
    case "mobile":
      return "phone";
    case "company":
    case "company_name":
    case "org":
    case "organization":
      return "company";
    case "title":
    case "job_title":
      return "title";
    case "city":
      return "city";
    case "country":
      return "country";
    default:
      return ["status", "source"].includes(normalizeHeader(header)) ? "none" : "custom";
  }
}

export function isCSVEmailValue(value: string): boolean {
  return looksLikeEmail(value);
}

export function buildCSVColumnMapping(
  headers: string[],
  selectedFields: Record<string, CSVContactField>,
  hasHeader: boolean,
): CSVColumnMapping {
  const columns = new Map<CSVContactField, string>();
  const mappedFields = new Set<CSVContactField>();
  const customKeys = new Set<string>();
  const customFields: Record<string, string> = {};

  for (const header of headers) {
    const field = selectedFields[header] ?? "none";
    if (field === "none") continue;
    if (field !== "custom" && mappedFields.has(field)) {
      throw new Error(`Map only one column to ${field.replace("_", " ")}.`);
    }
    mappedFields.add(field);

    if (field === "phone" || field === "company" || field === "title" || field === "city" || field === "country") {
      const key = field === "title" ? "job_title" : field;
      if (customKeys.has(key)) {
        throw new Error(`More than one column maps to the custom field "${key}".`);
      }
      customKeys.add(key);
      customFields[header] = key;
    } else if (field === "custom") {
      const key = normalizeHeader(header).slice(0, 63);
      if (!/^[a-z][a-z0-9_]{0,62}$/.test(key)) {
        throw new Error(`The custom field name for "${header}" is invalid.`);
      }
      if (customKeys.has(key)) {
        throw new Error(`More than one column maps to the custom field "${key}".`);
      }
      customKeys.add(key);
      customFields[header] = key;
    } else {
      columns.set(field, header);
    }
  }

  const email = columns.get("email");
  if (!email) throw new Error("Map a column to Email before continuing.");

  const mapping: CSVColumnMapping = { email, has_header: hasHeader };
  const firstName = columns.get("first_name");
  const lastName = columns.get("last_name");
  if (firstName) mapping.first_name = firstName;
  if (lastName) mapping.last_name = lastName;
  if (Object.keys(customFields).length > 0) mapping.custom_fields = customFields;
  return mapping;
}

function parseRecords(source: string, delimiter: string, maxRecords: number): string[][] {
  const records: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;

  for (let index = 0; index < source.length; index += 1) {
    const character = source[index];
    if (quoted) {
      if (character === '"' && source[index + 1] === '"') {
        cell += '"';
        index += 1;
      } else if (character === '"') {
        quoted = false;
      } else {
        cell += character;
      }
      continue;
    }

    if (character === '"' && cell.length === 0) {
      quoted = true;
    } else if (character === delimiter) {
      row.push(cell);
      cell = "";
    } else if (character === "\n" || character === "\r") {
      row.push(cell);
      records.push(row);
      row = [];
      cell = "";
      if (records.length >= maxRecords) return records;
      if (character === "\r" && source[index + 1] === "\n") index += 1;
    } else {
      cell += character;
    }
  }

  if (quoted) throw new Error("The CSV contains an unclosed quoted field.");
  if (cell.length > 0 || row.length > 0) {
    row.push(cell);
    records.push(row);
  }
  return records;
}

function normalizeHeader(value: string): string {
  return value
    .replace(/^\uFEFF/, "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
}

function looksLikeEmail(value: string): boolean {
  return /^[^\s@,;]+@[^\s@,;]+\.[^\s@,;]+$/.test(value.trim());
}
