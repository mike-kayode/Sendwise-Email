import type { Metadata } from "next";
import EditorShell from "./EditorShell";

export const metadata: Metadata = { title: "Campaign editor" };

export default async function CampaignEditorPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <EditorShell campaignId={id} />;
}
