<script setup>
// SkinPreview — small auto-rotating skinview3d canvas (PlayersPage rows,
// Dashboard upload preview).
// v2 (2026-10-26): modelType prop (default/slim) hot-swaps the player model;
// dispose on unmount so the context budget stays honest (grid pages must not
// accumulate live WebGL contexts).
// v3 (2026-11-01): the no-WebGL fallback lives here. Every consumer used to
// re-implement "canvas is empty, show a flat PNG" — or, worse, left a blank box
// that looked like a broken skin.
// v4 (2026-11-02): capeUrl. The server renderer does not draw capes, so the
// only place a cape can show is here.
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'

const props = defineProps({
  url: { type: String, default: '' },
  capeUrl: { type: String, default: '' },
  modelType: { type: String, default: 'default' },
  width: { type: Number, default: 128 },
  height: { type: Number, default: 192 },
})
const emit = defineEmits(['gl-unavailable'])
const canvas = ref(null)
// glLost is why the template branches: the page still gets `gl-unavailable` to
// reflow its layout, but it never has to guess how to draw one preview.
const glLost = ref(false)
let viewer = null

function webglOK() {
  try {
    const probe = document.createElement('canvas')
    return !!(probe.getContext('webgl2') || probe.getContext('webgl'))
  } catch { return false }
}

async function ensureViewer() {
  if (viewer || !canvas.value) return
  if (!webglOK()) {
    glLost.value = true
    emit('gl-unavailable')
    return
  }
  try {
    const skinview3d = await import('skinview3d')
    viewer = new skinview3d.SkinViewer({
      canvas: canvas.value,
      width: props.width,
      height: props.height,
      zoom: 0.7,
    })
    // A spinning preview is the one thing on the page that never stops asking for
    // the GPU, and 「reduced motion」 is how people opt out of exactly that.
    viewer.autoRotate = !window.matchMedia('(prefers-reduced-motion: reduce)').matches
    viewer.playerModelType = props.modelType === 'slim' ? 'slim' : 'default'
  } catch {
    // The probe can pass while the real context still fails (blocklisted GPU,
    // exhausted context budget, headless drivers). Fall back exactly like the
    // no-WebGL branch instead of leaking a raw TypeError from an async call.
    viewer = null
    glLost.value = true
    emit('gl-unavailable')
  }
}

async function apply() {
  await ensureViewer()
  if (!viewer) return
  if (props.url) {
    try { await viewer.loadSkin(props.url) } catch { /* broken url */ }
  }
  await showCape()
}

// loadCape(null) is the documented "hide it": a player whose cape was just
// taken off must not keep wearing the previous one.
async function showCape() {
  if (!viewer) return
  if (!props.capeUrl) { viewer.loadCape(null); return }
  try { await viewer.loadCape(props.capeUrl) } catch { /* broken url */ }
}

function applyModel() {
  if (viewer) viewer.playerModelType = props.modelType === 'slim' ? 'slim' : 'default'
}

onMounted(apply)
watch(() => props.url, apply)
watch(() => props.capeUrl, showCape)
watch(() => props.modelType, applyModel)
onBeforeUnmount(() => { if (viewer) { viewer.dispose(); viewer = null } })
</script>

<template>
  <img v-if="glLost && url" :src="url" alt=""
       :style="`width:${width}px;height:${height}px;object-fit:contain;image-rendering:pixelated;background:transparent;`" />
  <canvas v-else ref="canvas" style="background: transparent;" />
</template>
