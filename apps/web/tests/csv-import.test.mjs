import assert from "node:assert/strict";
import test from "node:test";
import {
  autoMapCSVHeader,
  buildCSVColumnMapping,
  parseCSVPreview,
  readCSVFile,
} from "../lib/csv-import.ts";

test("CSV decoding accepts UTF-8 BOM and Windows-1252 content", async () => {
  const utf8File = new File(["\uFEFFemail\nada@example.com\n"], "utf8.csv");
  assert.equal(await readCSVFile(utf8File), "email\nada@example.com\n");

  const windows1252File = new File(
    [Uint8Array.from([0x65, 0x6d, 0x61, 0x69, 0x6c, 0x2c, 0x63, 0x69, 0x74, 0x79, 0x0a, 0x61, 0x40, 0x65, 0x2e, 0x63, 0x6f, 0x6d, 0x2c, 0x4d, 0x6f, 0x6e, 0x74, 0x72, 0xe9, 0x61, 0x6c])],
    "windows-1252.csv",
  );
  assert.equal(await readCSVFile(windows1252File), "email,city\na@e.com,Montréal");
});

test("CSV preview handles UTF-8 BOM, semicolons, quoted commas, and multiline fields", () => {
  const preview = parseCSVPreview(
    "\uFEFFEmail Address;First Name;Company\nada@example.com;Ada;\"Engines, Inc.\nR&D\"\n",
  );
  assert.deepEqual(preview.headers, ["Email Address", "First Name", "Company"]);
  assert.deepEqual(preview.rows, [["ada@example.com", "Ada", "Engines, Inc.\nR&D"]]);
  assert.equal(preview.hasHeader, true);
});

test("headerless email files keep their first row and receive a usable email mapping", () => {
  const preview = parseCSVPreview("ada@example.com\r\ngrace@example.com\r\n");
  assert.deepEqual(preview.headers, ["Column 1"]);
  assert.deepEqual(preview.rows, [["ada@example.com"], ["grace@example.com"]]);
  assert.equal(preview.hasHeader, false);
  assert.equal(autoMapCSVHeader("Email Address"), "email");
});

test("headerless files can have the email column after other fields", () => {
  const preview = parseCSVPreview("Ada,ada@example.com\nGrace,grace@example.com\n");
  assert.deepEqual(preview.headers, ["Column 1", "Column 2"]);
  assert.equal(preview.hasHeader, false);
  assert.deepEqual(preview.rows[0], ["Ada", "ada@example.com"]);
  assert.equal(
    buildCSVColumnMapping(
      preview.headers,
      { "Column 1": "first_name", "Column 2": "email" },
      preview.hasHeader,
    ).email,
    "Column 2",
  );
});

test("column mapping uses the API contract and maps optional fields as custom fields", () => {
  const mapping = buildCSVColumnMapping(
    ["Email Address", "First Name", "Phone Number", "Company Name"],
    {
      "Email Address": "email",
      "First Name": "first_name",
      "Phone Number": "phone",
      "Company Name": "company",
    },
    true,
  );
  assert.deepEqual(mapping, {
    email: "Email Address",
    first_name: "First Name",
    custom_fields: {
      "Phone Number": "phone",
      "Company Name": "company",
    },
    has_header: true,
  });
  assert.throws(
    () => buildCSVColumnMapping(["One", "Two"], { One: "email", Two: "email" }, true),
    /Map only one column to email/,
  );
});

test("custom columns receive validated, stable field keys", () => {
  const mapping = buildCSVColumnMapping(
    ["Email", "Job Title", "Custom Preference"],
    { Email: "email", "Job Title": "title", "Custom Preference": "custom" },
    true,
  );
  assert.deepEqual(mapping.custom_fields, {
    "Job Title": "job_title",
    "Custom Preference": "custom_preference",
  });
  assert.equal(autoMapCSVHeader("Preferred language"), "custom");
});

test("fullname,email CSV headers map to the full-name contact field", () => {
  const preview = parseCSVPreview("fullname,email\nMike Bright,mikebright283@gmail.com\n");
  assert.equal(preview.hasHeader, true);
  assert.deepEqual(preview.headers, ["fullname", "email"]);
  assert.deepEqual(preview.rows, [["Mike Bright", "mikebright283@gmail.com"]]);
  assert.deepEqual(
    Object.fromEntries(preview.headers.map((header) => [header, autoMapCSVHeader(header)])),
    { fullname: "full_name", email: "email" },
  );
  assert.deepEqual(
    buildCSVColumnMapping(
      preview.headers,
      { fullname: "full_name", email: "email" },
      preview.hasHeader,
    ),
    { email: "email", full_name: "fullname", has_header: true },
  );
});
