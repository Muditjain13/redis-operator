# Screenshots and GIF to capture for the README

Everything below is captured by hand from the live portal. The README already has
the image lines, commented out, in its "Live demo" section, so nothing shows a
broken image until you add the files.

## Files to produce

| File (save in `docs/assets/`) | What it shows | Where it goes in the README |
|---|---|---|
| `demo.gif` | The main one. Press **Delete the StatefulSet** and watch the Pods go from Terminating to Pending to Ready until the recovery time appears in "How long it took". | Top of "Live demo", first image |
| `portal-ui.png` | The whole page at rest: header with the green "Live" marker, the cluster summary reading 3 / 3, three Ready Pod cards, and the events list. | "Live demo", under the GIF |
| `portal-buttons.png` | A close crop of the "Break something" panel with all five buttons visible. | "Live demo", under the screenshot above |
| `architecture.png` (optional) | A render of the Mermaid diagram, only if you want a picture for places that do not render Mermaid (LinkedIn, a PDF). GitHub renders the diagram itself, so the README does not need it. | Not used by default. If you want it, add an image line under the diagram in "How it works". |

## Before you start

- [ ] Use the live site, https://redisoperator.dpdns.org, so the capture shows the real deployment.
- [ ] If the name does not load on your own network, turn on Secure DNS in the browser (Chrome: Settings, Privacy and security, Security, Use secure DNS). Your router redirects normal DNS and fails for `dpdns.org`. Mobile data also works. Details are in `docs/hosting.md`, step 7.
- [ ] Wait until the page shows 3 / 3 ready and Redis 7.2. The portal resets itself every 10 minutes, so you can also just wait for that.
- [ ] Browser window about 1280 pixels wide, zoom 100 percent, light theme (the portal has no dark mode).
- [ ] Close bookmarks bars, extra tabs and anything personal that would show in the frame. Crop the browser chrome out if you can.
- [ ] You get one action per 30 seconds. Do the stills first, then the GIF, and wait out the cooldown between attempts.

## 1. `portal-ui.png`

- [ ] Capture the full page at rest, from the title to the last event.
- [ ] Check that the Pods read **Ready**, the summary reads **3 / 3**, and the events list has entries.
- [ ] Export as PNG, about 1280 pixels wide.

## 2. `portal-buttons.png`

- [ ] Scroll the "Break something" panel into view and crop tightly around it.
- [ ] All five buttons must be readable and none disabled (no cooldown message showing).

## 3. `demo.gif`

- [ ] Start recording with the page at rest.
- [ ] Press **Delete the StatefulSet**.
- [ ] Keep recording until all three Pods are Ready again and the new entry appears under "How long it took" (the full recovery takes roughly half a minute).
- [ ] Trim the dead time at the start and end. Aim for 20 to 40 seconds.
- [ ] Keep the file under about 10 MB (GitHub renders larger GIFs, but the README page gets slow). Reduce the width to 900 to 1000 pixels or the frame rate to 10 to 12 if it is too big.
- [ ] A screen recorder that exports GIFs works (for example ScreenToGif on Windows). Or record a video and convert it with `ffmpeg`.

## 4. `architecture.png` (optional)

- [ ] Open the README on GitHub, where the Mermaid diagram is rendered, and screenshot just the diagram. Or paste the diagram's source into the Mermaid live editor and export a PNG.

## After capturing

- [ ] Put the files in `docs/assets/` with exactly the names above.
- [ ] In `README.md`, "Live demo" section, delete the opening `<!-- Screenshots and the GIF go here ...` comment line (and the second line of that comment) and the closing `-->` line, so the three image lines are no longer inside a comment.
- [ ] Open the README preview and check all three images load and look sharp.
- [ ] Commit the images and the README change together.
