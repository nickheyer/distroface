import { browser } from '$app/environment';

export interface User {
    username: string;
    groups: string[];
}

export interface AuthResponse {
    token: string;
    expires_in: number;
    issued_at: string;
}

// STATE
const authState = $state({
    token: null as string | null,
    user: null as User | null,
    isAuthenticated: false,
    isLoading: false,
    error: null as string | null,
    baseUrl: import.meta.env.VITE_API_URL || ''
});

// COMPUTED
const userGroups = $derived(() => authState.user?.groups || []);
function hasRole(role: string): boolean {
    return userGroups().includes(role);
}

// ACTIONS
async function login(username: string, password: string): Promise<void> {
  authState.isLoading = true;
  authState.error = null;
  
  try {
      // USE THE WEBUI LOGIN ENDPOINT INSTEAD OF TOKEN ENDPOINT
      const response = await fetch(`${authState.baseUrl}/api/v1/auth/login`, {
          method: 'POST',
          headers: {
              'Content-Type': 'application/json'
          },
          body: JSON.stringify({ username, password })
      });

      if (!response.ok) {
          throw new Error('Authentication failed');
      }

      const data = await response.json();
      
      // UPDATE STATE WITH USER INFO
      authState.token = data.token;
      authState.user = {
          username: data.username,
          groups: data.groups
      };
      authState.isAuthenticated = true;
      
      if (browser) {
          localStorage.setItem('auth_token', data.token);
          localStorage.setItem('auth_username', data.username);
          localStorage.setItem('auth_base_url', authState.baseUrl);
      }
      
  } catch (err) {
      authState.error = err instanceof Error ? err.message : 'Login failed';
      authState.token = null;
      authState.isAuthenticated = false;
      authState.user = null;
  } finally {
      authState.isLoading = false;
  }
}

async function fetchUserInfo(): Promise<void> {
    if (!authState.token) return;
    
    try {
        const response = await fetch('/api/v1/users/me', {
            headers: {
                'Authorization': `Bearer ${authState.token}`
            }
        });
        
        if (!response.ok) throw new Error('Failed to fetch user info');

        authState.user = await response.json();
    } catch (err) {
        authState.error = err instanceof Error ? err.message : 'Failed to fetch user info';
        logout();
    }
}

function logout(): void {
    authState.token = null;
    authState.user = null;
    authState.isAuthenticated = false;
    
    if (browser) {
        localStorage.removeItem('auth_token');
        localStorage.removeItem('auth_username');
    }
}

async function handleResponse(response: Response) {
    if (response.status === 401) {
        // TOKEN EXPIRED
        logout();
        throw new Error("Your session has expired. Please log in again.");
    }
    
    if (response.status === 403) {
        throw new Error("You don't have permission to perform this action.");
    }
    
    if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'An error occurred');
    }
    
    return response;
}

// INITIALIZE AUTH
if (browser) {
    const storedToken = localStorage.getItem('auth_token');
    if (storedToken) {
        authState.token = storedToken;
        authState.isAuthenticated = true;
        fetchUserInfo();
    }
}

export {
    login,
    logout,
    hasRole
};

export const auth = {
    get token() { return authState.token },
    get user() { return authState.user },
    get isAuthenticated() { return authState.isAuthenticated },
    get isLoading() { return authState.isLoading },
    get error() { return authState.error },
    logout,
    login,
    hasRole
};

export const api = {
    async get(url: string) {
        const response = await fetch(`${authState.baseUrl}${url}`, {
            headers: {
                Authorization: `Bearer ${authState.token}`
            }
        });
        return handleResponse(response);
    },
    
    async post(url: string, data: any) {
        const response = await fetch(url, {
            method: 'POST',
            headers: {
                Authorization: `Bearer ${authState.token}`,
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(data)
        });
        return handleResponse(response);
    },
    
    async put(url: string, data: any) {
        const response = await fetch(url, {
            method: 'PUT',
            headers: {
                Authorization: `Bearer ${authState.token}`,
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(data)
        });
        return handleResponse(response);
    },
    
    async delete(url: string) {
        const response = await fetch(url, {
            method: 'DELETE',
            headers: {
                Authorization: `Bearer ${authState.token}`
            }
        });
        return handleResponse(response);
    }
};
