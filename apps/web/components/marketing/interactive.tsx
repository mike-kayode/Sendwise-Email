"use client";

import { useRef, useState } from "react";

export type CodeExample = {
  label: string;
  file?: string;
  code: string;
  note?: string;
};

async function copyToClipboard(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    return;
  } catch {
    const textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.select();
    const copied = document.execCommand("copy");
    textarea.remove();
    if (!copied) throw new Error("Clipboard access is unavailable.");
  }
}

export function CopyCodeTabs({
  examples,
  ariaLabel,
  dark = true,
}: {
  examples: CodeExample[];
  ariaLabel: string;
  dark?: boolean;
}) {
  const [activeIndex, setActiveIndex] = useState(0);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const tabs = useRef<Array<HTMLButtonElement | null>>([]);
  const active = examples[activeIndex];

  const selectTab = (index: number) => {
    setActiveIndex(index);
    setCopyState("idle");
  };
  const onTabKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    const direction = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!direction) return;
    event.preventDefault();
    const next = (activeIndex + direction + examples.length) % examples.length;
    selectTab(next);
    tabs.current[next]?.focus();
  };
  const copy = async () => {
    try {
      await copyToClipboard(active.code);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  };

  return (
    <div className={`codewin${dark ? "" : " codewin-light"}`}>
      <div className="codewin-bar">
        {dark ? <><span className="dot" /><span className="dot" /><span className="dot" /></> : null}
        <div className="tabs" role="tablist" aria-label={ariaLabel}>
          {examples.map((example, index) => (
            <button
              key={example.label}
              ref={(node) => { tabs.current[index] = node; }}
              type="button"
              role="tab"
              id={`code-tab-${ariaLabel.replaceAll(/\s+/g, "-")}-${index}`}
              aria-selected={activeIndex === index}
              aria-controls={`code-panel-${ariaLabel.replaceAll(/\s+/g, "-")}`}
              tabIndex={activeIndex === index ? 0 : -1}
              className={`tab${activeIndex === index ? " on" : ""}`}
              onClick={() => selectTab(index)}
              onKeyDown={onTabKeyDown}
            >
              {example.label}
            </button>
          ))}
        </div>
        <button type="button" className="copy" onClick={() => void copy()}>
          {copyState === "copied" ? "Copied" : "Copy"}
        </button>
      </div>
      {copyState === "failed" ? <p className="copy-status" role="status">Copy failed. Select the code and copy it manually.</p> : null}
      <div
        role="tabpanel"
        id={`code-panel-${ariaLabel.replaceAll(/\s+/g, "-")}`}
        aria-labelledby={`code-tab-${ariaLabel.replaceAll(/\s+/g, "-")}-${activeIndex}`}
        tabIndex={0}
      >
        {active.file ? <div className="code-file mono">{active.file}</div> : null}
        <pre className="code code-content">{active.code}</pre>
      </div>
      {active.note ? <p className="small code-note">{active.note}</p> : null}
    </div>
  );
}

export function FAQAccordion({
  items,
  label,
}: {
  items: ReadonlyArray<{ question: string; answer: string }>;
  label: string;
}) {
  const [openIndex, setOpenIndex] = useState<number | null>(0);
  const id = label.toLowerCase().replaceAll(/\s+/g, "-");
  return (
    <div className="faq-list" aria-label={label}>
      {items.map((item, index) => {
        const open = index === openIndex;
        const questionID = `${id}-question-${index}`;
        const answerID = `${id}-answer-${index}`;
        return (
          <div className="faq-item" key={item.question}>
            <h3 className="faq-heading">
              <button
                type="button"
                id={questionID}
                className="faq-q"
                aria-expanded={open}
                aria-controls={answerID}
                onClick={() => setOpenIndex(open ? null : index)}
              >
                <span>{item.question}</span>
                <span aria-hidden="true" className="faq-sign">{open ? "−" : "+"}</span>
              </button>
            </h3>
            <div
              id={answerID}
              className="faq-a"
              role="region"
              aria-labelledby={questionID}
              hidden={!open}
            >
              {item.answer}
            </div>
          </div>
        );
      })}
    </div>
  );
}
