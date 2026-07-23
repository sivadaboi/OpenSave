// Svelte action for modal backdrops.
//
// Dismiss the modal only when a click both *starts* and *ends* on the
// backdrop element itself. Plain `on:click|self` closes whenever a click
// resolves to the backdrop — including when the press begins inside the
// modal (e.g. clicking into a search field and dragging to place the cursor
// or select text) and the mouse is released over the backdrop, because the
// browser then fires the click on the nearest common ancestor. That made
// modals dismiss intermittently while the user was interacting with them.
//
// Usage:
//   <div class="backdrop" use:backdropClose={onClose} role="presentation">
export function backdropClose(node, onClose) {
  let pressedOnSelf = false;

  const onMousedown = (e) => {
    pressedOnSelf = e.target === node;
  };
  const onClick = (e) => {
    if (pressedOnSelf && e.target === node) onClose?.();
    pressedOnSelf = false;
  };

  node.addEventListener('mousedown', onMousedown);
  node.addEventListener('click', onClick);

  return {
    update(next) {
      onClose = next;
    },
    destroy() {
      node.removeEventListener('mousedown', onMousedown);
      node.removeEventListener('click', onClick);
    }
  };
}
