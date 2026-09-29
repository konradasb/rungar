// Copyright (c) 2023 Xin, Hextra; changes copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT
//
// Hextra's js/core/code-copy.js (v0.12.3), changed so that the button on a
// console block copies only its commands: without the $ prompts, and without
// the output shown after them. Keep in step with Hextra when upgrading it.

// Copy button for code blocks

// The commands in a console block: each line that starts with the $ prompt,
// with the prompt taken off, and the lines that continue it after a \. A
// block with no prompt in it is copied as it is.
const consoleCommands = (code) => {
  const lines = code.split('\n');
  if (!lines.some((line) => line.startsWith('$ '))) {
    return code;
  }
  const commands = [];
  let continued = false;
  for (const line of lines) {
    if (line.startsWith('$ ')) {
      commands.push(line.slice(2));
    } else if (continued) {
      commands.push(line);
    } else {
      continue;
    }
    continued = line.trimEnd().endsWith('\\');
  }
  return commands.join('\n') + '\n';
}

document.addEventListener('DOMContentLoaded', function () {
  const getCopyIcon = () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.innerHTML = `
      <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
    `;
    svg.setAttribute('fill', 'none');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', '2');
    return svg;
  }

  const getSuccessIcon = () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.innerHTML = `
      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
    `;
    svg.setAttribute('fill', 'none');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', '2');
    return svg;
  }

  // Make scrollable code blocks focusable for keyboard users.
  const updateScrollableCodeBlocks = () => {
    document.querySelectorAll('.hextra-code-block pre, .highlight pre').forEach(function (pre) {
      if (pre.scrollWidth > pre.clientWidth) {
        pre.setAttribute('tabindex', '0');
      } else {
        pre.removeAttribute('tabindex');
      }
    });
  };

  updateScrollableCodeBlocks();

  let resizeRaf;
  window.addEventListener('resize', () => {
    if (resizeRaf) {
      cancelAnimationFrame(resizeRaf);
    }
    resizeRaf = requestAnimationFrame(updateScrollableCodeBlocks);
  });

  document.querySelectorAll('.hextra-code-copy-btn').forEach(function (button) {
    // Add copy and success icons
    button.querySelector('.hextra-copy-icon')?.appendChild(getCopyIcon());
    button.querySelector('.hextra-success-icon')?.appendChild(getSuccessIcon());

    // Add click event listener for copy button
    button.addEventListener('click', function (e) {
      e.preventDefault();
      // Get the code target
      const target = button.parentElement.previousElementSibling;
      let codeElement;
      if (target.tagName === 'CODE') {
        codeElement = target;
      } else {
        // Select the last code element in case line numbers are present
        const codeElements = target.querySelectorAll('code');
        codeElement = codeElements[codeElements.length - 1];
      }
      if (codeElement) {
        let code = codeElement.innerText;
        // Replace double newlines with single newlines in the innerText
        // as each line inside <span> has trailing newline '\n'
        if ("lang" in codeElement.dataset) {
          code = code.replace(/\n\n/g, '\n');
        }
        if (codeElement.dataset.lang === 'console') {
          code = consoleCommands(code);
        }
        navigator.clipboard.writeText(code).then(function () {
          button.classList.add('copied');
          var originalLabel = button.getAttribute('aria-label');
          var copiedLabel = button.dataset.copiedLabel || 'Copied!';
          button.setAttribute('aria-label', copiedLabel);
          setTimeout(function () {
            button.classList.remove('copied');
            button.setAttribute('aria-label', originalLabel);
          }, 1000);
        }).catch(function (err) {
          console.error('Failed to copy text: ', err);
        });
      } else {
        console.error('Target element not found');
      }
    });
  });
});
