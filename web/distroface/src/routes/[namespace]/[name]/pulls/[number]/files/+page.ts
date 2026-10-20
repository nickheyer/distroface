import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = ({ params }) => {
	redirect(302, `/${params.namespace}/${params.name}/pulls/${params.number}?tab=files`);
};
