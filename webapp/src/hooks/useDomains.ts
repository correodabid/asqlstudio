import { useCallback, useEffect, useState } from 'react'
import { api } from '../lib/api'

export function useDomains(reloadKey = 0) {
  const [domains, setDomains] = useState<string[]>([])
  const [loading, setLoading] = useState(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const resp = await api<{ domains: string[] }>('/api/domains', 'GET')
      setDomains(resp.domains || [])
    } catch {
      // leave previous
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    refresh()
  }, [refresh, reloadKey])

  return { domains, loading, refresh }
}
