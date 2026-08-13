import { Routes, Route } from 'react-router-dom'
import { Sidebar } from './components/Sidebar'
import { Dashboard } from './pages/Dashboard'
import { Accounts } from './pages/Accounts'
import { Transactions } from './pages/Transactions'
import { ImportStatement } from './pages/ImportStatement'
import { Bills } from './pages/Bills'
import { Transfers } from './pages/Transfers'
import { Settings } from './pages/Settings'
import { Placeholder } from './pages/Placeholder'

export default function App() {
  return (
    <div className="flex min-h-screen bg-[var(--color-bg)] text-[var(--color-text)]">
      <Sidebar />
      <main className="flex-1 min-w-0">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/accounts" element={<Accounts />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/import" element={<ImportStatement />} />
          <Route path="/bills" element={<Bills />} />
          <Route path="/transfers" element={<Transfers />} />
          <Route path="/settings" element={<Settings />} />
          <Route
            path="/investments"
            element={<Placeholder title="Investments" note="Coming soon — track stocks, mutual funds & more." />}
          />
        </Routes>
      </main>
    </div>
  )
}
