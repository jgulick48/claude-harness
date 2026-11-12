// Terminal color presets. "match-app" has no entry: it means "derive colors
// from the app's CSS tokens" (theme: undefined). Every value below comes from
// the scheme's published palette.
import type { ITheme } from '@xterm/xterm'

export interface ThemePreset {
  id: string
  label: string
  /** Where the palette comes from. */
  source: string
  theme: ITheme
}

export const MATCH_APP = 'match-app'

function ansi(c: string[]): ITheme {
  const [black, red, green, yellow, blue, magenta, cyan, white, bBlack, bRed, bGreen, bYellow, bBlue, bMagenta, bCyan, bWhite] = c
  return {
    black, red, green, yellow, blue, magenta, cyan, white,
    brightBlack: bBlack, brightRed: bRed, brightGreen: bGreen, brightYellow: bYellow,
    brightBlue: bBlue, brightMagenta: bMagenta, brightCyan: bCyan, brightWhite: bWhite
  }
}

// Solarized by Ethan Schoonover (MIT): https://ethanschoonover.com/solarized/
const SOLARIZED_ANSI = [
  '#073642', '#dc322f', '#859900', '#b58900', '#268bd2', '#d33682', '#2aa198', '#eee8d5',
  '#002b36', '#cb4b16', '#586e75', '#657b83', '#839496', '#6c71c4', '#93a1a1', '#fdf6e3'
]

export const PRESETS: ThemePreset[] = [
  {
    id: 'solarized-dark',
    label: 'Solarized Dark',
    source: 'Solarized, Ethan Schoonover (MIT)',
    theme: { ...ansi(SOLARIZED_ANSI), background: '#002b36', foreground: '#839496', cursor: '#93a1a1', cursorAccent: '#002b36', selectionBackground: '#073642' }
  },
  {
    id: 'solarized-light',
    label: 'Solarized Light',
    source: 'Solarized, Ethan Schoonover (MIT)',
    theme: { ...ansi(SOLARIZED_ANSI), background: '#fdf6e3', foreground: '#657b83', cursor: '#586e75', cursorAccent: '#fdf6e3', selectionBackground: '#eee8d5' }
  },
  {
    id: 'tomorrow-night',
    label: 'Tomorrow Night',
    source: 'Tomorrow Theme, Chris Kempson (MIT)',
    theme: {
      ...ansi([
        '#1d1f21', '#cc6666', '#b5bd68', '#f0c674', '#81a2be', '#b294bb', '#8abeb7', '#c5c8c6',
        '#969896', '#cc6666', '#b5bd68', '#f0c674', '#81a2be', '#b294bb', '#8abeb7', '#ffffff'
      ]),
      background: '#1d1f21', foreground: '#c5c8c6', cursor: '#c5c8c6', cursorAccent: '#1d1f21', selectionBackground: '#373b41'
    }
  },
  {
    id: 'gruvbox-dark',
    label: 'Gruvbox Dark',
    source: 'gruvbox, Pavel Pertsev (MIT/X11)',
    theme: {
      ...ansi([
        '#282828', '#cc241d', '#98971a', '#d79921', '#458588', '#b16286', '#689d6a', '#a89984',
        '#928374', '#fb4934', '#b8bb26', '#fabd2f', '#83a598', '#d3869b', '#8ec07c', '#ebdbb2'
      ]),
      background: '#282828', foreground: '#ebdbb2', cursor: '#ebdbb2', cursorAccent: '#282828', selectionBackground: '#504945'
    }
  },
  {
    id: 'high-contrast-light',
    label: 'High Contrast Light',
    source: 'ANSI colors from the GitHub Light (Primer, MIT) terminal palette on pure black-on-white',
    theme: {
      ...ansi([
        '#24292f', '#cf222e', '#116329', '#4d2d00', '#0969da', '#8250df', '#1b7c83', '#6e7781',
        '#57606a', '#a40e26', '#1a7f37', '#633c01', '#218bff', '#a475f9', '#3192aa', '#8c959f'
      ]),
      background: '#ffffff', foreground: '#000000', cursor: '#000000', cursorAccent: '#ffffff', selectionBackground: '#a5c8f5'
    }
  }
]
