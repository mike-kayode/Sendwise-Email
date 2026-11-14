import { redirect } from "next/navigation";

export default function LegalIndexPage(): never {
  redirect("/legal/privacy");
}
