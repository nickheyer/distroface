<script lang="ts">
  import { Settings, Package, Server, Shield, Users, Key, Cog, ChartBarStacked } from "lucide-svelte";
  import { auth } from "$lib/stores/auth.svelte";
  import ArtifactSettings from "./ArtifactSettings.svelte";
  import RoleSettings from "./RoleSettings.svelte";
  import GroupSettings from "./GroupSettings.svelte";
  import AuthSettings from "./AuthSettings.svelte";
  import SystemSettings from "./SystemSettings.svelte";
  import MetricsPanel from "./MetricsPanel.svelte";

  let activeTab = $state("config");
  let loading = $state(false);
  let error = $state<string | null>(null);

  const sections = [
    {
      id: "config",
      title: "System Configuration",
      icon: Cog,
      description: "View system configuration",
      component: SystemSettings,
      roles: ['admins']
    },
    {
      id: "auth",
      title: "Authentication",
      icon: Key,
      description: "Security and access control settings",
      component: AuthSettings,
      roles: ['admins']
    },
    {
      id: "roles",
      title: "Roles",
      icon: Shield,
      description: "Manage system roles and permissions",
      component: RoleSettings,
      roles: ['admins']
    },
    {
      id: "groups",
      title: "Groups",
      icon: Users,
      description: "Manage user groups and access",
      component: GroupSettings,
      roles: ['admins']
    },
    {
      id: "artifacts",
      title: "Artifact Storage",
      icon: Package,
      description: "Configure artifact retention and storage policies",
      component: ArtifactSettings,
      roles: ['admins']
    },
    {
      id: "metrics",
      title: "Metrics Data",
      icon: ChartBarStacked,
      description: "Real-time performance monitoring",
      component: MetricsPanel,
      roles: ['admins']
    }
  ];

  // FILTER TABS BASED ON ROLE
  const availableSections = $derived(
    sections.filter(section => 
      section.roles.some(role => auth.hasRole(role))
    )
  );

  function isTabVisible(tabId: string): boolean {
    const section = sections.find(s => s.id === tabId);
    if (!section) return false;
    return section.roles.some(role => auth.hasRole(role));
  }
</script>

<div class="space-y-6">
  <div>
    <h1 class="text-2xl font-semibold text-gray-900">Settings</h1>
    <p class="text-sm text-gray-600">
      Manage system-wide configuration, settings, policies, and metrics
    </p>
  </div>

  <!-- TAB NAV -->
  <div class="border-b border-gray-200">
    <nav class="-mb-px flex space-x-8" aria-label="Tabs">
      {#each availableSections as section}
        <button
          class={`whitespace-nowrap border-b-2 py-4 px-1 text-sm font-medium transition-colors duration-200
            ${activeTab === section.id
              ? 'border-blue-500 text-blue-600'
              : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700'}`}
          onclick={() => (activeTab = section.id)}
        >
          <div class="flex items-center">
            <section.icon class="h-4 w-4 mr-2" />
            {section.title}
          </div>
        </button>
      {/each}
    </nav>
  </div>

  <!-- ERR -->
  {#if error}
    <div class="rounded-md bg-red-50 p-4">
      <div class="flex">
        <div class="ml-3">
          <h3 class="text-sm font-medium text-red-800">Error</h3>
          <div class="mt-2 text-sm text-red-700">
            <p>{error}</p>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <!-- CONTENT -->
  <div class="bg-white shadow-sm rounded-lg">
    {#each sections as section}
      {#if activeTab === section.id && isTabVisible(section.id)}
        <div class="p-6">
          <h2 class="text-lg font-medium text-gray-900">{section.title}</h2>
          <p class="mt-1 text-sm text-gray-500">{section.description}</p>

          {#if loading}
            <div class="flex justify-center py-12">
              <div
                class="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"
              ></div>
            </div>
          {:else if section.component}
            <div class="mt-6">
              <section.component/>
            </div>
          {/if}
        </div>
      {/if}
    {/each}
  </div>
</div>