import { reactRouter } from "@react-router/dev/vite"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

const apiTarget =
  process.env.API_PROXY_TARGET ||
  `http://localhost:${process.env.PORT || "20128"}`

export default defineConfig({
  resolve: { tsconfigPaths: true },
  plugins: [tailwindcss(), reactRouter()],
  server: {
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        proxyTimeout: 10000,
        timeout: 10000,
        configure(proxy) {
          proxy.on("proxyReq", (proxyReq, req) => {
            const cookie = req.headers.cookie
            if (!cookie) return

            const authCookie = cookie
              .split(";")
              .map((part) => part.trim())
              .find((part) => part.startsWith("auth_token="))

            if (authCookie) {
              proxyReq.setHeader("cookie", authCookie)
            } else {
              proxyReq.removeHeader("cookie")
            }
          })
        },
      },
    },
  },
})
