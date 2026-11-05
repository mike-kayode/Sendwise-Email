import type { Metadata } from "next";
import FormsView from "./FormsView";

export const metadata: Metadata = { title: "Forms" };

export default function FormsPage() {
  return <FormsView />;
}
