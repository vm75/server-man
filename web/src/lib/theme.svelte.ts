export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'server-man:theme'

class ThemeManager {
  current = $state<Theme>('dark')

  init() {
    if (typeof window === 'undefined') return
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved === 'dark' || saved === 'light') {
      this.set(saved)
    } else {
      const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
      this.set(prefersDark ? 'dark' : 'light')
    }

    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const listener = (e: MediaQueryListEvent) => {
      if (!localStorage.getItem(STORAGE_KEY)) {
        this.set(e.matches ? 'dark' : 'light')
      }
    }
    media.addEventListener('change', listener)
    return () => media.removeEventListener('change', listener)
  }

  set(theme: Theme) {
    this.current = theme
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', theme)
      document.documentElement.style.colorScheme = theme
      const meta = document.querySelector('meta[name="theme-color"]')
      if (meta) {
        meta.setAttribute('content', theme === 'dark' ? '#0b101b' : '#f2f6fc')
      }
    }
  }

  toggle() {
    const next: Theme = this.current === 'dark' ? 'light' : 'dark'
    this.set(next)
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {}
    return next
  }
}

export const themeState = new ThemeManager()
