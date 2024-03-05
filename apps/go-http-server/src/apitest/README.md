# Test API using rest-nvim

## How

rest-nvim follows the RFC 2616 request format.

Refer to this [documentation](https://github.com/rest-nvim/rest.nvim?tab=readme-ov-file#keybindings). By default rest.nvim does not have any key mappings so you will not have conflicts with any of your existing ones.

To run rest.nvim you should map the following commands:

<Plug>RestNvim, run the request under the cursor
<Plug>RestNvimPreview, preview the request cURL command
<Plug>RestNvimLast, re-run the last request

Find those mapping with `:Telescope keymaps`.
