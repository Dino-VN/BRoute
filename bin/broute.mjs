#!/usr/bin/env node
import { createReadStream, createWriteStream } from "node:fs"
import { chmod, mkdir, readFile, rm, writeFile } from "node:fs/promises"
import { homedir, platform, arch } from "node:os"
import path from "node:path"
import process from "node:process"
import { spawn } from "node:child_process"
import { pipeline } from "node:stream/promises"
import { createGunzip } from "node:zlib"
import * as tar from "tar"

const OWNER = process.env.BROUTE_GITHUB_OWNER || "Dino-VN"
const REPO = process.env.BROUTE_GITHUB_REPO || "BRoute"
const INSTALL_DIR = process.env.BROUTE_HOME || path.join(homedir(), ".broute")
const RELEASE_API = `https://api.github.com/repos/${OWNER}/${REPO}/releases/latest`

async function main() {
  const command = process.argv[2]
  if (command === "update") {
    await ensureInstalled({ force: true, interactive: false })
    return
  }
  if (command === "--version" || command === "version") {
    const state = await readState()
    console.log(state?.version || "not installed")
    return
  }

  await ensureInstalled({ force: false, interactive: process.stdout.isTTY && process.stdin.isTTY })
  await runBinary(process.argv.slice(2))
}

async function ensureInstalled({ force, interactive }) {
  await mkdir(INSTALL_DIR, { recursive: true })
  const state = await readState()
  const release = await latestRelease()
  const binaryPath = installedBinaryPath()
  const needsInstall = force || !state || state.version !== release.tag_name || !(await exists(binaryPath))
  if (!needsInstall) return

  if (state && state.version !== release.tag_name && interactive && !force) {
    const answer = await ask(`BRoute ${release.tag_name} is available (current ${state.version}). Update now? [Y/n] `)
    if (/^n(o)?$/i.test(answer.trim())) return
  } else if (state && state.version !== release.tag_name && !force) {
    console.error(`BRoute ${release.tag_name} is available. Run: npx broute update`)
    return
  }

  const asset = release.assets.find((item) => item.name === assetName())
  if (!asset) {
    throw new Error(`No release asset named ${assetName()} was found in ${OWNER}/${REPO}@${release.tag_name}`)
  }
  console.error(`Installing BRoute ${release.tag_name} into ${INSTALL_DIR}`)
  await installAsset(asset.browser_download_url)
  await writeFile(path.join(INSTALL_DIR, "version.json"), JSON.stringify({ version: release.tag_name, installedAt: new Date().toISOString() }, null, 2))
}

async function latestRelease() {
  const response = await fetch(RELEASE_API, { headers: { "User-Agent": "broute-npx" } })
  if (!response.ok) throw new Error(`Failed to check GitHub releases: ${response.status} ${await response.text()}`)
  return response.json()
}

async function installAsset(url) {
  const tmp = path.join(INSTALL_DIR, `download-${Date.now()}.tar.gz`)
  const response = await fetch(url, { headers: { "User-Agent": "broute-npx" } })
  if (!response.ok || !response.body) throw new Error(`Failed to download release asset: ${response.status}`)
  await pipeline(response.body, createWriteStream(tmp))
  await rm(path.join(INSTALL_DIR, "bin"), { recursive: true, force: true })
  await rm(path.join(INSTALL_DIR, "web"), { recursive: true, force: true })
  await pipeline(createReadStream(tmp), createGunzip(), tar.x({ cwd: INSTALL_DIR }))
  await rm(tmp, { force: true })
  await chmod(installedBinaryPath(), 0o755)
}

async function runBinary(args) {
  const env = { ...process.env, BROUTE_HOME: INSTALL_DIR }
  if (!env.DATA_DIR) env.DATA_DIR = path.join(INSTALL_DIR, "data")
  const child = spawn(installedBinaryPath(), args, { stdio: "inherit", env })
  child.on("exit", (code, signal) => {
    if (signal) process.kill(process.pid, signal)
    process.exit(code ?? 0)
  })
}

async function readState() {
  try {
    return JSON.parse(await readFile(path.join(INSTALL_DIR, "version.json"), "utf8"))
  } catch {
    return null
  }
}

async function exists(file) {
  try {
    await readFile(file)
    return true
  } catch {
    return false
  }
}

function installedBinaryPath() {
  return path.join(INSTALL_DIR, "bin", process.platform === "win32" ? "broute.exe" : "broute")
}

function assetName() {
  const os = platform()
  const cpu = arch() === "x64" ? "amd64" : arch()
  return `broute_${os}_${cpu}.tar.gz`
}

function ask(prompt) {
  return new Promise((resolve) => {
    process.stdout.write(prompt)
    process.stdin.resume()
    process.stdin.once("data", (data) => resolve(String(data)))
  })
}

main().catch((error) => {
  console.error(error.message)
  process.exit(1)
})