import type { NextConfig } from 'next'

const SECURITY_HEADERS = [
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  { key: 'Referrer-Policy', value: 'no-referrer' },
]

const nextConfig: NextConfig = {
  async headers() {
    return [
      // Security headers are cheap and apply everywhere, assets included.
      { source: '/:path*', headers: SECURITY_HEADERS },
      {
        // The API serves live machine state, so no CDN or browser may hold a
        // rendered snapshot of someone's local Flow state. `dynamic =
        // 'force-dynamic'` is the real guarantee — Next already sends
        // `private, no-cache, no-store` on a dynamic route — and this is the
        // belt to its braces.
        //
        // `_next/static` is excluded deliberately: those filenames are
        // content-addressed and carry no Flow data, and a blanket `no-store`
        // re-downloads every JS and CSS chunk on every page load.
        source: '/((?!_next/static/).*)',
        headers: [{ key: 'Cache-Control', value: 'no-store' }],
      },
    ]
  },
}

export default nextConfig
