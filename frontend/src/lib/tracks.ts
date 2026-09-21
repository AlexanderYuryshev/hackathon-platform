import { useQuery } from '@tanstack/react-query'
import { api } from '~/lib/api'
import type { Track } from '~/lib/types'

// Tracks of a hackathon, ordered by sort_order. Each track has its own
// scoring config and leaderboard.
export function useTracks(hackathonId: string | undefined) {
  return useQuery({
    queryKey: ['tracks', hackathonId],
    queryFn: () => api.get<{ tracks: Track[] }>(`/hackathons/${hackathonId}/tracks`),
    enabled: !!hackathonId,
    staleTime: 30_000,
  })
}

export function defaultTrackKey(tracks: Track[] | undefined): string {
  return tracks?.[0]?.key ?? ''
}
