import {
  type RouteConfig,
  index,
  layout,
  route,
} from "@react-router/dev/routes"

export default [
  route("login", "routes/login.tsx"),
  layout("components/console-layout.tsx", [
    index("routes/home.tsx"),
    route("api", "routes/api.tsx"),
    route("providers", "routes/providers.tsx"),
    route("providers/:providerId", "routes/provider-detail.tsx"),
    route("quota", "routes/provider-quota.tsx"),
    route("debug-logs", "routes/debug-logs.tsx"),
  ]),
] satisfies RouteConfig
