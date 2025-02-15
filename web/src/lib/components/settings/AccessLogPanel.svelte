<script lang="ts">
  import { api } from "$lib/stores/auth.svelte";
  import { Download, Search, AlertCircle, Loader2 } from "lucide-svelte";
  import { formatDistance } from "date-fns";
  
  interface AccessLogEntry {
    timestamp: string;
    username: string;
    action: string;
    resource: string;
    path: string;
    method: string;
    status: number;
  }

  let loading = $state(true);
  let error = $state<string | null>(null);
  let logs = $state<AccessLogEntry[]>([]);
  let searchTerm = $state("");
  let statusFilter = $state<number | null>(null);
  let userFilter = $state("");
  
  const defaultInterval = 10 * 1000; // POLLING EVERY 10 SEC
  let pollInterval = $state<ReturnType<typeof setInterval> | null>(null);

  // FILTERED LOGS
  const filteredLogs = $derived(
    logs.filter(log => {
      const matchesSearch = 
        log.path.toLowerCase().includes(searchTerm.toLowerCase()) ||
        log.action.toLowerCase().includes(searchTerm.toLowerCase()) ||
        log.resource.toLowerCase().includes(searchTerm.toLowerCase());
        
      const matchesStatus = statusFilter === null || log.status === statusFilter;
      const matchesUser = !userFilter || log.username === userFilter;
      
      return matchesSearch && matchesStatus && matchesUser;
    })
  );

  // FILTER USERS AND STATUSES
  const uniqueUsers = $derived([...new Set(logs.map(log => log.username))]);
  const uniqueStatuses = $derived([...new Set(logs.map(log => log.status))]);

  async function fetchLogs() {
    try {
      const response = await api.get("/api/v1/settings/metrics");
      const data = await response.json();
      logs = data.access_logs || [];
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to fetch access logs";
    } finally {
      loading = false;
    }
  }

  function getStatusColor(status: number): string {
    if (status < 300) return "text-green-600";
    if (status < 400) return "text-blue-600";
    if (status < 500) return "text-yellow-600";
    return "text-red-600";
  }

  function downloadLogs() {
    const csv = [
      ['Timestamp', 'Username', 'Action', 'Resource', 'Path', 'Method', 'Status'],
      ...filteredLogs.map(log => [
        log.timestamp,
        log.username,
        log.action,
        log.resource,
        log.path,
        log.method,
        log.status
      ])
    ].map(row => row.join(',')).join('\n');

    const blob = new Blob([csv], { type: 'text/csv' });
    const url = window.URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `access_logs_${new Date().toISOString()}.csv`;
    document.body.appendChild(a);
    a.click();
    window.URL.revokeObjectURL(url);
    document.body.removeChild(a);
  }

  // INIT
  $effect(() => {
    fetchLogs();
    pollInterval = setInterval(fetchLogs, defaultInterval);
    
    return () => {
      if (pollInterval) clearInterval(pollInterval);
    };
  });
</script>

<div class="space-y-6">
  <!-- HEADER -->
  <div class="sm:flex sm:items-center sm:justify-between">
    <button
      onclick={downloadLogs}
      class="mt-4 sm:mt-0 inline-flex items-center px-4 py-2 border border-gray-300 rounded-md shadow-sm text-sm font-medium text-gray-700 bg-white hover:bg-gray-50"
    >
      <Download class="h-4 w-4 mr-2" />
      Export CSV
    </button>
  </div>

  <!-- SEARCH AND FILTERS -->
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-4">
    <div class="sm:col-span-2">
      <div class="relative">
        <input
          type="text"
          placeholder="Search logs..."
          bind:value={searchTerm}
          class="block w-full pl-10 pr-3 py-2 border border-gray-300 rounded-md focus:ring-blue-500 focus:border-blue-500 sm:text-sm"
        />
        <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
      </div>
    </div>
    
    <div>
      <select
        bind:value={statusFilter}
        class="block w-full pl-3 pr-10 py-2 text-base border-gray-300 focus:outline-none focus:ring-blue-500 focus:border-blue-500 sm:text-sm rounded-md"
      >
        <option value={null}>All Status Codes</option>
        {#each uniqueStatuses as status}
          <option value={status}>{status}</option>
        {/each}
      </select>
    </div>
    
    <div>
      <select
        bind:value={userFilter}
        class="block w-full pl-3 pr-10 py-2 text-base border-gray-300 focus:outline-none focus:ring-blue-500 focus:border-blue-500 sm:text-sm rounded-md"
      >
        <option value="">All Users</option>
        {#each uniqueUsers as user}
          <option value={user}>{user}</option>
        {/each}
      </select>
    </div>
  </div>

  <!-- ERROR STATE -->
  {#if error}
    <div class="rounded-md bg-red-50 p-4">
      <div class="flex">
        <AlertCircle class="h-5 w-5 text-red-400" />
        <div class="ml-3">
          <h3 class="text-sm font-medium text-red-800">Error</h3>
          <div class="mt-2 text-sm text-red-700">
            <p>{error}</p>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <!-- LOADING STATE -->
  {#if loading}
    <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
  {:else}
    <!-- LOG TABLE -->
    <div class="bg-white shadow overflow-hidden rounded-lg">
      <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Time
              </th>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                User
              </th>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Action
              </th>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Resource
              </th>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Method
              </th>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Path
              </th>
              <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Status
              </th>
            </tr>
          </thead>
          <tbody class="bg-white divide-y divide-gray-200">
            {#each filteredLogs as log}
              <tr class="hover:bg-gray-50">
                <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
                  {formatDistance(new Date(log.timestamp), new Date(), { addSuffix: true })}
                </td>
                <td class="px-6 py-4 whitespace-nowrap">
                  <div class="text-sm font-medium text-gray-900">{log.username}</div>
                </td>
                <td class="px-6 py-4 whitespace-nowrap">
                  <span class="px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-blue-100 text-blue-800">
                    {log.action}
                  </span>
                </td>
                <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
                  {log.resource}
                </td>
                <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
                  {log.method}
                </td>
                <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
                  {log.path}
                </td>
                <td class="px-6 py-4 whitespace-nowrap text-sm">
                  <span class={getStatusColor(log.status)}>
                    {log.status}
                  </span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</div>
