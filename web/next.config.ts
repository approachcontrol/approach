import type { NextConfig } from 'next'

const nextConfig: NextConfig = {
  // The API serves live machine state; nothing this app renders is cacheable.
  // Route-level `no-store` fetches already enforce that; this keeps CDN caches
  // from holding a rendered snapshot of someone's local Flow state.
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'Cache-Control', value: 'no-store' },
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'Referrer-Policy', value: 'no-referrer' },
        ],
      },
    ]
  },
}

export default nextConfig
