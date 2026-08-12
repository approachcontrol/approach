import type { Metadata } from 'next'
import Link from 'next/link'

import './globals.css'

export const metadata: Metadata = {
  title: 'Approach',
  description: 'Read-only view of Approach repositories and Flows',
  // A deployed instance renders machine-local state; keep it out of indexes
  // even when Deployment Protection is off.
  robots: { index: false, follow: false },
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <div className="shell">
          <header className="masthead">
            <span className="masthead__title">
              <Link href="/">approach</Link>
            </span>
            <span className="masthead__tag">repositories &amp; flows</span>
          </header>
          <main>{children}</main>
        </div>
      </body>
    </html>
  )
}
