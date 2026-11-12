import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles.css'
// M6: one stylesheet per area so features don't collide in styles.css.
import './sidebar.css'
import './layout.css'
import './appearance.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
)
