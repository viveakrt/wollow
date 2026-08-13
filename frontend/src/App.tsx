import { Navigate, Outlet, Route, Routes } from 'react-router-dom'
import { RequireAuth, RedirectIfAuthed } from './platform/components/RequireAuth'
import { AppShell } from './platform/components/AppShell'
import { LoginPage } from './pages/LoginPage'
import { SettingsPage } from './pages/SettingsPage'

import { InboxPage } from './products/mail/pages/InboxPage'
import { MessageDetailPage } from './products/mail/pages/MessageDetailPage'
import { ConnectAccountPage } from './products/mail/pages/ConnectAccountPage'

import { MoneySidebar } from './products/money/components/MoneySidebar'
import { Dashboard } from './products/money/pages/Dashboard'
import { Accounts } from './products/money/pages/Accounts'
import { Transactions } from './products/money/pages/Transactions'
import { ImportStatement } from './products/money/pages/ImportStatement'
import { Bills } from './products/money/pages/Bills'
import { Transfers } from './products/money/pages/Transfers'
import { MoneySettings } from './products/money/pages/MoneySettings'
import { Placeholder } from './products/money/pages/Placeholder'

/**
 * Mail manages its own full-height panes (account sidebar, message list, detail),
 * so the shell hands it the frame and stays out of the way.
 */
function MailLayout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  )
}

/** Money is a single scrolling column beside a fixed product sidebar. */
function MoneyLayout() {
  return (
    <AppShell sidebar={<MoneySidebar />}>
      <div className="h-full w-full overflow-y-auto">
        <Outlet />
      </div>
    </AppShell>
  )
}

/** Settings is platform-level, so it gets the rail but no product sidebar. */
function PlatformLayout() {
  return (
    <AppShell>
      <div className="h-full w-full overflow-y-auto">
        <Outlet />
      </div>
    </AppShell>
  )
}

export default function App() {
  return (
    <Routes>
      <Route
        path="/login"
        element={
          <RedirectIfAuthed>
            <LoginPage />
          </RedirectIfAuthed>
        }
      />

      <Route
        element={
          <RequireAuth>
            <MailLayout />
          </RequireAuth>
        }
      >
        <Route path="/mail" element={<InboxPage />} />
        <Route path="/mail/accounts/new" element={<ConnectAccountPage />} />
        <Route path="/mail/messages/:accountId/:messageId" element={<MessageDetailPage />} />
      </Route>

      <Route
        element={
          <RequireAuth>
            <MoneyLayout />
          </RequireAuth>
        }
      >
        <Route path="/money" element={<Dashboard />} />
        <Route path="/money/accounts" element={<Accounts />} />
        <Route path="/money/transactions" element={<Transactions />} />
        <Route path="/money/transfers" element={<Transfers />} />
        <Route path="/money/bills" element={<Bills />} />
        <Route path="/money/import" element={<ImportStatement />} />
        <Route path="/money/settings" element={<MoneySettings />} />
        <Route
          path="/money/investments"
          element={
            <Placeholder title="Investments" note="Coming soon — track stocks, mutual funds & more." />
          }
        />
      </Route>

      <Route
        element={
          <RequireAuth>
            <PlatformLayout />
          </RequireAuth>
        }
      >
        <Route path="/settings" element={<SettingsPage />} />
      </Route>

      <Route path="/" element={<Navigate to="/mail" replace />} />
      <Route path="*" element={<Navigate to="/mail" replace />} />
    </Routes>
  )
}
