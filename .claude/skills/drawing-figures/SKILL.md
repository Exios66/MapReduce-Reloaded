---
name: drawing-figures
description: Use when drawing, redrawing or reviewing any figure or chart in MapReduce-Reloaded (bench/evaluation, examples/demo.ipynb, testdata, docs), choosing its colors, or when labels collide, a title crowds the panels, a figure exists in only one theme, a caption's number is wrong, or make figures-check reports differing pixels.
---

# Drawing figures

The `dataviz` skill gives the method (form, color, marks, checks). This
skill gives the project's values for it and the tool that applies them. It
also lists the mistakes made here before. Load `dataviz` first, then follow
the recipe below.

## Recipe: what a figure here is made of, in order

1. **Numbers from the data, checked.** Compute every number and claim the
   title, subtitle or a label states, and `assert` each claim beside the
   code that states it, e.g. `assert kafka["die"] > english["die"]` before a
   caption saying "mostly Kafka's". Assert, too, that the data you plot
   equals the published data (sum the inputs and compare with
   `expected/`, or read the experiment's `runs.csv`).
2. **The house style, loaded.**

   ```python
   import sys

   sys.path.insert(0, ".claude/skills/drawing-figures")
   import figkit
   import matplotlib.pyplot as plt

   kit = figkit.style()  # bench/plot-experiments.py's THEMES, rc, heading, dots, sizes; draws nothing
   for name, t in kit.THEMES.items():  # light, then dark: every figure ships in both
       with plt.rc_context(kit.rc(t)):
           fig, ax = plt.subplots(figsize=(10, 4.5))  # at most 10 inches wide
           ax.plot(xs, ys, color=t["series"][0])
           kit.dots(ax, t, [x_end], [y_end], t["series"][0])
           kit.heading(fig, t, f"{finding}", "What is measured; how to read the marks", key=None)
           figkit.finish(kit, fig, out / f"NAME{figkit.suffix(name)}.png")
   ```

3. **A title that states the finding**, computed, and a subtitle that says
   what is measured and how to read the marks. `kit.heading` stacks them
   by their drawn heights, so text never crowds the panels.
4. **`figkit.finish`, never `fig.savefig` directly.** It lays the figure
   out as the evaluation figures are, then refuses to save, naming each
   problem, if two labels overlap, a label covers a marker, or text runs
   off the image. Move the label it names (offset, `ha`, or which side of
   its point) and draw again.
5. **Look at both PNGs** (Read the image) before calling the figure done.
   The check finds collisions, not a label beside the wrong point, a
   misleading scale or empty space.
6. **A table of the numbers beside it.** The PNG has no hover, so the
   page that shows it also gives its values: the README's results table,
   a Markdown table, or the CSV the figure reads.

## Project values

| Parameter | Value |
| --- | --- |
| Surfaces | light `#fcfcfb`, dark `#1a1a19` (`kit.THEMES[...]["surface"]`) |
| Text | `t["ink"]` titles, `t["ink2"]` labels and values, `t["muted"]` notes; never a series color |
| Series | `t["series"]`: blue, orange, aqua. Blue is the shipped or undisturbed setting, orange the changed one. A series keeps its slot in every panel. |
| Fourth series | `figkit.FOURTH[name]` (yellow), only where neighbors are adjacent (stacked bars, lines), with a legend and direct labels. Never in a scatter, dot plot or small multiples: beside orange it fails the all-pairs color checks. Past four, fold into "Other" or facet. |
| Low contrast | aqua and yellow are under 3:1 on the light surface: label them directly, or give the table |
| Font | Atkinson Hyperlegible from `bench/fonts`, registered by `figkit.style()` |
| Sizes | `kit.TITLE` 17, `kit.SUBTITLE` 11.5, `kit.PANEL` 12.5, `kit.LABEL` 11.5, `kit.TICK` and `kit.NOTE` 10.5; 200 dpi |
| Files | `NAME.png` and `NAME-dark.png`, side by side; a page shows the one for its reader's theme |
| Palette check | the dataviz skill's validator, in that skill's own scripts folder: `python3 validate_palette.py "HEX,..." --mode light` and `--mode dark --surface "#1a1a19"`; add `--pairs all` for scatter and dot plots |

## Evaluation figures (`bench/evaluation/`)

They are drawn only by `bench/plot-experiments.py`, from the archives in
`bench/evaluation/raw/`. A new experiment gets its own draw function there,
both PNGs, a README results row and a test, in the same PR as its data.
`make figures-check PLOT_PYTHON=...` redraws every figure and compares
pixels; it matches only on the experiment VM, whose matplotlib build and
font rendering drew them. A difference elsewhere is the machine, not the
data: never commit figures redrawn off the VM.

## Mistakes made here, and their fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| A caption's count or claim is wrong ("`die` is all Kafka" when 7 are English) | Written from what the data should say | Recipe step 1: compute it, assert it |
| Labels on a log-log line collide, and moving them by guess takes rounds | Points close on a log axis sit close in pixels | `figkit.finish` names the collision; put neighbors on opposite sides (`ha="right"` with a negative offset, `ha="left"` with a positive one) |
| A label covers its own dot | `ha="left"` with a leftward offset runs the text back over the point | Offset and `ha` point the same way |
| Gap under the title, or the title floats away from the panels | `fig.suptitle` plus `fig.text` plus `tight_layout(rect=...)` guessed by hand | `kit.heading`, then `figkit.finish` |
| Type too small or set in DejaVu Sans | Own `rcParams`, or the fonts not registered | `kit.rc(t)` inside `plt.rc_context`, after `figkit.style()` |
| Only a light figure | One theme drawn | Loop over `kit.THEMES` |
| Importing `bench/plot-experiments.py` redraws `bench/results/` | The script runs `main()` when imported | `figkit.style()`, which points `main` at an empty directory |
| A figure is missing for new data | The data merged, the draw function not | The data, the draw function and both PNGs go in one PR |
| `make figures-check` reports every pixel differing | Run off the VM | Run it on the VM; see above |
