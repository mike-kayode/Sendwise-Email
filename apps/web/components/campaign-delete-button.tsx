"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { apiFetch, type Campaign } from "@/lib/auth";
import { useAuthStore } from "@/stores/auth";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/ui/use-toast";

type CampaignDeleteButtonProps = {
  campaign: Pick<Campaign, "id" | "name" | "status">;
  onDeleted?: () => void;
};

const deletableStatuses = new Set(["draft", "ready", "cancelled"]);
const campaignManagers = new Set(["owner", "admin", "manager"]);

export function CampaignDeleteButton({ campaign, onDeleted }: CampaignDeleteButtonProps) {
  const role = useAuthStore((state) => state.organization?.role);
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const deletion = useMutation({
    mutationFn: () => apiFetch<void>(`/campaigns/${campaign.id}`, { method: "DELETE" }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["campaigns"] });
      toast({ title: "Campaign deleted", description: `"${campaign.name}" was deleted.` });
      onDeleted?.();
    },
    onError: (error) => {
      toast({
        title: "Could not delete campaign",
        description: error instanceof Error ? error.message : "The campaign could not be deleted.",
        variant: "destructive",
      });
    },
  });

  if (!campaignManagers.has(role ?? "") || !deletableStatuses.has(campaign.status)) {
    return null;
  }

  const remove = () => {
    if (deletion.isPending || !window.confirm(`Permanently delete "${campaign.name}"? This cannot be undone.`)) {
      return;
    }
    deletion.mutate();
  };

  return (
    <Button
      type="button"
      variant="danger"
      size="sm"
      aria-label={`Delete campaign ${campaign.name}`}
      disabled={deletion.isPending}
      onClick={remove}
    >
      <Trash2 className="h-4 w-4" />
      {deletion.isPending ? "Deleting..." : "Delete"}
    </Button>
  );
}
