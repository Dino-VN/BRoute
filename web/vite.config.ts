import { reactRouter } from "@react-router/dev/vite"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

export default defineConfig({
  resolve: { tsconfigPaths: true },
  plugins: [tailwindcss(), reactRouter()],
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:20128",
        changeOrigin: true,
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
