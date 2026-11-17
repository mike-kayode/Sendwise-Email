"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Ban } from "lucide-react";
import { apiFetch, type Campaign } from "@/lib/auth";
import { useAuthStore } from "@/stores/auth";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/ui/use-toast";

type CampaignCancelButtonProps = {
  campaign: Pick<Campaign, "id" | "name" | "status">;
};

const cancellableStatuses = new Set(["draft", "ready", "scheduled", "processing", "sending", "paused"]);
const campaignManagers = new Set(["owner", "admin", "manager"]);

export function CampaignCancelButton({ campaign }: CampaignCancelButtonProps) {
  const role = useAuthStore((state) => state.organization?.role);
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const cancellation = useMutation({
    mutationFn: () => apiFetch<{ status: string }>(`/campaigns/${campaign.id}/cancel`, { method: "POST" }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["campaigns"] }),
        queryClient.invalidateQueries({ queryKey: ["campaign", campaign.id] }),
      ]);
      toast({ title: "Campaign cancelled", description: `"${campaign.name}" will not send any remaining messages.` });
    },
    onError: (error) => {
      toast({
        title: "Could not cancel campaign",
        description: error instanceof Error ? error.message : "The campaign could not be cancelled.",
        variant: "destructive",
      });
    },
  });

  if (!campaignManagers.has(role ?? "") || !cancellableStatuses.has(campaign.status)) {
    return null;
  }

  const cancel = () => {
    if (cancellation.isPending || !window.confirm(`Cancel "${campaign.name}"? Any remaining messages will be skipped.`)) {
      return;
    }
    cancellation.mutate();
  };

  return (
    <Button
      type="button"
      variant="secondary"
      size="sm"
      aria-label={`Cancel campaign ${campaign.name}`}
      disabled={cancellation.isPending}
      onClick={cancel}
    >
      <Ban className="h-4 w-4" />
      {cancellation.isPending ? "Cancelling..." : "Cancel"}
    </Button>
  );
}
