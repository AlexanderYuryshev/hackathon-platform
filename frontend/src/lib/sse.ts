import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

const FALLBACK_POLL_MS = 30_000

export function useLeaderboardEvents(hackathonId: string | undefined, trackKey: string, enabled: boolean) {
  const queryClient = useQueryClient()
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    if (!hackathonId || !trackKey || !enabled || typeof window === 'undefined') {
      return
    }
    let source: EventSource | null = new EventSource(
      `/api/hackathons/${hackathonId}/leaderboard/events?track=${encodeURIComponent(trackKey)}`,
    )
    let fallbackTimer: ReturnType<typeof setInterval> | null = null

    const invalidate = () => {
      void queryClient.invalidateQueries({ queryKey: ['leaderboard', hackathonId] })
      void queryClient.invalidateQueries({ queryKey: ['team-detail', hackathonId] })
    }

    const startFallbackPoll = () => {
      if (fallbackTimer) return
      fallbackTimer = setInterval(invalidate, FALLBACK_POLL_MS)
    }
    const stopFallbackPoll = () => {
      if (fallbackTimer) {
        clearInterval(fallbackTimer)
        fallbackTimer = null
      }
    }

    source.onopen = () => {
      setConnected(true)
      stopFallbackPoll()
    }
    source.onerror = () => {
      setConnected(false)
      // EventSource retries transient failures by itself; if the connection is
      // gone for good (e.g. proxy error), keep data fresh with polling.
      if (source?.readyState === EventSource.CLOSED) {
        startFallbackPoll()
      }
    }

    source.addEventListener('leaderboard.updated', invalidate)
    source.addEventListener('team.updated', invalidate)
    source.addEventListener('scoring.completed', invalidate)
    source.addEventListener('scoring.error', () => {
      void queryClient.invalidateQueries({ queryKey: ['leaderboard', hackathonId] })
    })
    // heartbeat only proves the connection is alive — it must not trigger refetches

    return () => {
      source?.close()
      source = null
      stopFallbackPoll()
      setConnected(false)
    }
  }, [hackathonId, trackKey, enabled, queryClient])

  return { connected }
}
