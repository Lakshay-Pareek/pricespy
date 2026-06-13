import { useState, useCallback } from 'react'
import axios from 'axios'

const API = import.meta.env.VITE_API_BASE || ''

const DEMO_ACTIONS = [
  {
    id: 'flash-sale',
    label: '↯ Flash Sale — iPhone',
    endpoint: '/api/simulate/flash-sale',
    method: 'POST',
    toastMessage: 'Flash sale triggered on iPhone 15 — price dropped up to 25%!',
    toastType: 'success',
    toastIcon: '↯',
  },
  {
    id: 'out-of-stock',
    label: '○ Out of Stock — Sony',
    endpoint: '/api/simulate/out-of-stock',
    method: 'POST',
    toastMessage: 'Sony WH-1000XM5 marked as Out of Stock — price +8%',
    toastType: 'warning',
    toastIcon: '○',
  },
  {
    id: 'competitor-drop',
    label: '↕ Competitor Drop',
    endpoint: '/api/simulate/competitor-drop',
    method: 'POST',
    toastMessage: 'Competitor price drop triggered — cascading to similar products',
    toastType: 'info',
    toastIcon: '↕',
  },
  {
    id: 'fast-forward',
    label: '» Fast Forward 24h',
    endpoint: '/api/simulate/fast-forward?hours=24',
    method: 'POST',
    toastMessage: 'Fast-forwarded 24 hours — 48 new price data points generated',
    toastType: 'info',
    toastIcon: '»',
  },
]

const COOLDOWN_MS = 5000

export default function DemoControls({ onRefresh, onToast }) {
  const [loading, setLoading] = useState({})
  const [success, setSuccess] = useState({})
  const [cooldown, setCooldown] = useState({})

  const handleAction = useCallback(
    async (action) => {
      if (cooldown[action.id] || loading[action.id]) return

      setLoading((prev) => ({ ...prev, [action.id]: true }))
      try {
        await axios({ method: action.method, url: `${API}${action.endpoint}` })
        setSuccess((prev) => ({ ...prev, [action.id]: true }))

        // Show toast
        onToast?.({
          message: action.toastMessage,
          type: action.toastType,
          icon: action.toastIcon,
          duration: 3000,
        })

        // Trigger product list refresh
        onRefresh?.()

        // Clear success after 2s
        setTimeout(() => {
          setSuccess((prev) => ({ ...prev, [action.id]: false }))
        }, 2000)

      } catch (err) {
        console.error(`Demo action failed: ${action.id}`, err)
        onToast?.({
          message: `Demo action failed: ${err.message}`,
          type: 'error',
          icon: '!',
        })
      } finally {
        setLoading((prev) => ({ ...prev, [action.id]: false }))

        // 5-second cooldown to prevent spam
        setCooldown((prev) => ({ ...prev, [action.id]: true }))
        setTimeout(() => {
          setCooldown((prev) => ({ ...prev, [action.id]: false }))
        }, COOLDOWN_MS)
      }
    },
    [onRefresh, onToast, cooldown, loading]
  )

  return (
    <div className="demo-controls-sidebar">
      <div className="text-subhead" style={{ marginBottom: '8px', fontSize: '11px' }}>
        DEMO CONTROLS
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
        {DEMO_ACTIONS.map((action) => {
          const isLoading = loading[action.id]
          const isSuccess = success[action.id]
          const isCooldown = cooldown[action.id]
          const isDisabled = isLoading || isCooldown

          return (
            <button
              key={action.id}
              id={`demo-${action.id}`}
              onClick={() => handleAction(action)}
              disabled={isDisabled}
              className="demo-btn"
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
              }}
            >
              <span>
                {action.label}
                {isCooldown && !isLoading && (
                  <span style={{ fontSize: '9px', color: 'var(--text-muted)', marginLeft: '4px' }}>
                    (cooldown)
                  </span>
                )}
              </span>

              {isLoading && (
                <span className="text-mono" style={{ fontSize: '10px' }}>...</span>
              )}

              {isSuccess && !isLoading && (
                <span style={{ color: 'var(--green)' }}>ok</span>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}
