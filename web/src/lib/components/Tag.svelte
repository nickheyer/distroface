<script lang="ts">
  import { Tag as TagIcon, Copy, Check } from 'lucide-svelte';
  import { copyToClipboard } from '@svelte-put/copy';
  import { onDestroy } from 'svelte';

  let { name, tag, onClick = null, selected = false } = $props();
  let showCopySuccess = $state(false);
  let copyTimeout: ReturnType<typeof setTimeout>;

  function handleCopy() {
    copyToClipboard(`docker pull ${window.location.host}/${name}:${tag}`);
    showCopySuccess = true;
    
    if (copyTimeout) clearTimeout(copyTimeout);
    
    copyTimeout = setTimeout(() => {
      showCopySuccess = false;
    }, 2000);
  }

  onDestroy(() => {
    if (copyTimeout) clearTimeout(copyTimeout);
  });
</script>

<span class="inline-flex items-center group">
  {#if onClick}
    <button 
      type="button" 
      onclick={onClick}
      class="focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 rounded-l"
    >
      <div class="inline-flex items-center px-2.5 py-1.5 border rounded-l text-xs font-medium
                  {selected ? 'bg-blue-100 text-blue-800 border-blue-300' : 'bg-gray-100 text-gray-800 border-gray-200 hover:bg-blue-200'}">
        <TagIcon class="h-3 w-3 mr-1" />
        {tag}
      </div>
    </button>
  {:else}
    <div class="inline-flex items-center px-2.5 py-1.5 border rounded-l text-xs font-medium
                {selected ? 'bg-blue-100 text-blue-800 border-blue-300' : 'bg-gray-100 text-gray-800 border-gray-200'}">
      <TagIcon class="h-3 w-3 mr-1" />
      {tag}
    </div>
  {/if}
  
  <button
    type="button"
    onclick={handleCopy}
    class="inline-flex items-center px-1.5 py-1.5 border-l-0 border rounded-r text-xs 
           {showCopySuccess ? 'bg-green-50 text-green-600' : 'bg-gray-50 text-gray-600'}
           hover:bg-gray-100 hover:text-gray-900
           active:bg-gray-200 active:text-gray-800
           border-gray-200 transition-colors
           focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2"
    title={showCopySuccess ? 'Copied!' : 'Copy pull command'}
  >
    {#if showCopySuccess}
      <Check class="h-3 w-3" />
    {:else}
      <Copy class="h-3 w-3" />
    {/if}
  </button>
</span>