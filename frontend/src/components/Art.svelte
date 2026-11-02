<script lang="ts">
  // The identity drawings (fork D, phase 5; APP.md §6): the folded sheet,
  // the app's mark, drawn large where Enfold waits for the user or has
  // nothing yet to show, and the open box of a vault with no archives. They
  // are line drawings in the app's tokens (app.css, "the identity
  // drawings"): the outline and the folds in the accent at 1.6 px, two
  // whole device pixels at 125 %, the crease and the lines of writing at
  // 1.2, the body in the accent's wash, the flap in --mark-flap. No motion
  // of their own and no binary.
  //   stage  the lock stage's mark, 96 px wide, its box cropped to the
  //          figure's 72 px of height, so the panel gains the 24 px of empty
  //          box that stood above and below the hexagon
  //   first  the same mark at 128 px, on the first run's stage
  //   box    the open box, 72 px, above the sentence of an empty vault
  // Every <svg> below is internal/brand's geometry as its tests draw it
  // (art_test.go: Sheet(96).Cropped(72), Sheet(128) and the open box on the
  // sheet's grid and slope), compared whole, whitespace aside. Edit the
  // geometry and paste what go test -v -run TestArtMarkup ./internal/brand
  // logs, never the numbers here: a vertical moved off the 0.8 px grid
  // smears over three device pixels.
  interface Props {
    kind: "stage" | "first" | "box";
  }
  let { kind }: Props = $props();
</script>

{#if kind === "stage"}
  <svg class="idart" data-art="stage" width="96" height="72" viewBox="0 0 96 72" aria-hidden="true">
    <path class="art-fill" d="M17.6 18.449 48 0.897 78.4 18.449 78.4 53.551 48 71.103 17.6 53.551Z" />
    <path class="art-flap" d="M48 0.897 78.4 18.449 48 36Z" />
    <path class="art-crease" stroke-width="1.2" d="M78.4 18.449 48 36" />
    <path class="art-thin" stroke-width="1.2" d="M25.6 31.867 43.2 42.029M25.6 39.067 43.2 49.229M25.6 46.267 36.16 52.364" />
    <path class="art-line" stroke-width="1.6" d="M17.6 18.449 48 0.897 78.4 18.449 78.4 53.551 48 71.103 17.6 53.551ZM48 0.897 48 36M17.6 18.449 78.4 53.551" />
  </svg>
{:else if kind === "first"}
  <svg class="idart" data-art="first" width="128" height="128" viewBox="0 0 128 128" aria-hidden="true">
    <path class="art-fill" d="M23.2 40.444 64 16.888 104.8 40.444 104.8 87.556 64 111.112 23.2 87.556Z" />
    <path class="art-flap" d="M64 16.888 104.8 40.444 64 64Z" />
    <path class="art-crease" stroke-width="1.2" d="M104.8 40.444 64 64" />
    <path class="art-thin" stroke-width="1.2" d="M33.867 58.336 57.6 72.038M33.867 67.936 57.6 81.638M33.867 77.536 48.107 85.757" />
    <path class="art-line" stroke-width="1.6" d="M23.2 40.444 64 16.888 104.8 40.444 104.8 87.556 64 111.112 23.2 87.556ZM64 16.888 64 64M23.2 40.444 104.8 87.556" />
  </svg>
{:else}
  <svg class="idart" data-art="box" width="72" height="72" viewBox="0 0 72 72" aria-hidden="true">
    <path class="art-paper" d="M12.8 30.4 36 17.005 28.8 6.605 5.6 20ZM36 17.005 59.2 30.4 66.4 20 43.2 6.605Z" />
    <path class="art-inside" d="M12.8 30.4 36 17.005 59.2 30.4 36 43.795Z" />
    <path class="art-fill" d="M12.8 30.4 36 43.795 36 66.195 12.8 52.8ZM36 43.795 59.2 30.4 59.2 52.8 36 66.195Z" />
    <path class="art-thin" stroke-width="1.2" d="M36 17.005 36 30.893" />
    <path class="art-line" stroke-width="1.6" d="M12.8 30.4 36 17.005 28.8 6.605 5.6 20ZM36 17.005 59.2 30.4 66.4 20 43.2 6.605ZM12.8 30.4 36 43.795 36 66.195 12.8 52.8ZM36 43.795 59.2 30.4 59.2 52.8 36 66.195Z" />
  </svg>
{/if}
