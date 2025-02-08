import { auth } from './auth.svelte';

export interface Group {
    name: string;
    description: string;
    roles: string[];
    scope: string;
}

// STATE
const groupState = $state({
    groups: [] as Group[],
    loading: false,
    error: null as string | null,
    initialized: false
});

// COMPUTED
const availableGroups = $derived(() => 
    groupState.groups.map(group => ({
        ...group,
        // MAKE SURE WE FORMAT GROUPS CORRECTLY
        name: group.name.toLowerCase(),
    }))
);

// ACTIONS
async function fetchGroups() {
    groupState.loading = true;
    groupState.error = null;
    
    try {
        const response = await fetch('/api/v1/groups', {
            headers: {
                'Authorization': `Bearer ${auth.token}`
            }
        });

        if (!response.ok) {
            throw new Error('Failed to fetch groups');
        }
        
        const data = await response.json();

        // FORCE REACTIVITY
        groupState.groups = [...data.map((group: Group) => ({
            ...group,
            name: group.name.toLowerCase(),
        }))];

        groupState.initialized = true;
    } catch (err) {
        groupState.error = err instanceof Error ? err.message : 'Failed to load groups';
        throw err;
    } finally {
        groupState.loading = false;
    }
}

function isValidGroup(groupName: string): boolean {
    return groupState.groups.some(g => g.name.toLowerCase() === groupName.toLowerCase());
}

export const groups = {
    get all(): Group[] { return availableGroups() },
    get loading() { return groupState.loading },
    get error() { return groupState.error },
    fetchGroups,
    isValidGroup
};
