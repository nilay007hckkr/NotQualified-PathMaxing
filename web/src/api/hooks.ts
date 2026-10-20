import { useQuery } from '@tanstack/react-query';
import { api } from './client';

// Shared queries for pickers. Campaigns and tags are optional extras in the
// link screens, so callers hide their controls when these fail instead of
// blocking the page.

export const campaignsKey = ['campaigns'] as const;
export const tagsKey = ['tags'] as const;

export function useCampaigns() {
  return useQuery({ queryKey: campaignsKey, queryFn: () => api.campaigns.list(), select: (p) => p.items });
}

export function useTags() {
  return useQuery({ queryKey: tagsKey, queryFn: () => api.tags.list(), select: (p) => p.items });
}
