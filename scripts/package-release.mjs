#!/usr/bin/env node
import { mkdir, rm, cp } from "node:fs/promises"
import { spawnSync } from "node:child_process"
import { arch, platform } from "node:os"
import path from "node:path"
import process from "node:process"
import * as tar from "tar"

const root = process.cwd()
const dist = path.join(root, "dist")
const version = process.env.VERSION || "dev"

run("npm", ["install"], { cwd: path.join(root, "web") })
run("npm", ["run", "build"], { cwd: path.join(root, "web") })

await rm(dist, { recursive: true, force: true })
await mkdir(dist, { recursive: true })

const targets = (process.env.TARGETS || currentTarget()).split(",").map((target) => target.trim()).filter(Boolean)
for (const target of targets) {
  const [goos, goarch] = target.split("/")
  if (!goos || !goarch) throw new Error(`Invalid target: ${target}`)
  const stage = path.join(dist, `broute_${goos}_${goarch}`)
  await mkdir(path.join(stage, "bin"), { recursive: true })
  await mkdir(path.join(stage, "web"), { recursive: true })
  const binary = path.join(stage, "bin", goos === "windows" ? "broute.exe" : "broute")
  run("go", ["build", "-ldflags", `-s -w -X broute/internal/config.Version=${version}`, "-o", binary, "./cmd/broute"], {
    cwd: root,
    env: { ...process.env, CGO_ENABLED: process.env.CGO_ENABLED || "1", GOOS: goos, GOARCH: goarch },
  })
  await cp(path.join(root, "web", "build", "client"), path.join(stage, "web"), { recursive: true })
  await tar.c({ gzip: true, cwd: stage, file: path.join(dist, `broute_${goos}_${goarch}.tar.gz`) }, ["bin", "web"])
}

function currentTarget() {
  const os = platform()
  const cpu = arch() === "x64" ? "amd64" : arch()
  return `${os}/${cpu}`
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { stdio: "inherit", ...options })
  if (result.status !== 0) process.exit(result.status ?? 1)
}