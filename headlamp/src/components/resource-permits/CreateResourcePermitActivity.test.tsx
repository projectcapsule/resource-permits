// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CreateResourcePermit, ResourcePermitSetupForm } from './CreateResourcePermitActivity';

const mocks = vi.hoisted(() => ({
  close: vi.fn(),
  post: vi.fn(),
  push: vi.fn(),
  enqueueSnackbar: vi.fn(),
  namespaceList: vi.fn(),
  globalTemplateList: vi.fn(),
  namespacedTemplateList: vi.fn(),
  template: {
    spec: { paramSchema: { type: 'object', properties: {} } },
    status: { namespaces: ['*'] },
  },
}));
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  Activity: { close: mocks.close },
  ApiProxy: { post: mocks.post },
  K8s: { ResourceClasses: { Namespace: { useList: mocks.namespaceList } } },
  Router: {
    createRouteURL: (_: string, p: any) =>
      `/c/${p.cluster}/customresources/${p.crd}/${p.namespace}/${p.crName}`,
  },
}));
vi.mock('react-router-dom', () => ({ useHistory: () => ({ push: mocks.push }) }));
vi.mock('notistack', () => ({ useSnackbar: () => ({ enqueueSnackbar: mocks.enqueueSnackbar }) }));
vi.mock('../../resources/resourcePermits', () => ({
  GlobalResourcePermitTemplate: {
    useGet: () => [mocks.template, null],
    useList: mocks.globalTemplateList,
  },
  ResourcePermitTemplate: {
    useGet: () => [mocks.template, null],
    useList: mocks.namespacedTemplateList,
  },
}));
vi.mock('../common/AnchoredSectionBox', () => ({
  AnchoredSectionBox: ({ children }: any) => <div>{children}</div>,
}));
vi.mock('../common/CapsuleResourceLink', () => ({
  CapsuleResourceLink: ({ children }: any) => <span>{children}</span>,
}));
vi.mock('./ResourcePermitTemplatePresentation', () => ({
  ResourcePermitTemplateIdentity: () => null,
}));
vi.mock('./ResourcePermitYamlDialog', () => ({ ResourcePermitYamlDialog: () => null }));
vi.mock('./KubernetesResourceSchemaFieldInput', () => ({
  KubernetesResourceSchemaWidget: () => null,
}));

describe('ResourcePermit setup', () => {
  const visibleNamespaces = (names: string[]) => names.map(name => ({ getName: () => name }));

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.template = {
      spec: { paramSchema: { type: 'object', properties: {} } },
      status: { namespaces: ['*'] },
    };
    mocks.namespaceList.mockReturnValue([visibleNamespaces(['solar-test']), null]);
    mocks.globalTemplateList.mockReturnValue([[], null]);
    mocks.namespacedTemplateList.mockReturnValue([[], null]);
  });

  it.each([
    { reported: ['*'], scope: [] },
    { reported: ['unseen-namespace'], scope: ['solar-test'] },
    { reported: [], scope: ['solar-test'] },
    { reported: undefined, scope: [] },
  ])(
    'lists only visible namespaces with template availability $reported',
    ({ reported, scope }) => {
      mocks.template.status = { namespaces: reported as any };
      mocks.namespaceList.mockReturnValue([
        visibleNamespaces(['solar-test', 'solar-dev', 'solar-test']),
        null,
      ]);
      render(
        <ResourcePermitSetupForm
          cluster="main"
          namespaces={scope}
          templateKind="GlobalResourcePermitTemplate"
          templateName="grafana"
        />
      );
      expect(mocks.namespaceList).toHaveBeenCalledWith({ cluster: 'main' });
      fireEvent.mouseDown(screen.getByRole('combobox', { name: 'Namespace' }));
      expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual([
        'solar-dev',
        'solar-test',
      ]);
      expect(screen.queryByRole('textbox', { name: 'Namespace' })).toBeNull();
    }
  );

  it('uses the same visible namespace list for a namespaced template', () => {
    mocks.namespaceList.mockReturnValue([visibleNamespaces(['solar-dev', 'solar-test']), null]);
    render(
      <ResourcePermitSetupForm
        cluster="main"
        namespaces={['solar-test']}
        templateKind="ResourcePermitTemplate"
        templateName="access"
        templateNamespace="unseen-namespace"
      />
    );
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'Namespace' }));
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual([
      'solar-dev',
      'solar-test',
    ]);
  });

  it.each([
    { items: null, error: null, message: 'Loading visible Namespaces…' },
    { items: [], error: null, message: 'No Namespaces are visible to this account.' },
    {
      items: null,
      error: new Error('Forbidden'),
      message: 'Visible Namespaces could not be loaded. Check Namespace list access.',
    },
  ])('keeps a disabled selector when $message', ({ items, error, message }) => {
    mocks.namespaceList.mockReturnValue([items, error]);
    render(
      <ResourcePermitSetupForm
        cluster="main"
        namespaces={['solar-test']}
        templateKind="GlobalResourcePermitTemplate"
        templateName="grafana"
      />
    );
    expect(screen.getByRole('combobox', { name: 'Namespace' }).getAttribute('aria-disabled')).toBe(
      'true'
    );
    expect(screen.getByText(message)).toBeTruthy();
    expect(screen.queryByRole('textbox', { name: 'Namespace' })).toBeNull();
    fireEvent.change(screen.getByLabelText('ResourcePermit name prefix', { exact: false }), {
      target: { value: 'incident' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Template Parameters' }));
    expect(screen.queryByRole('button', { name: 'Create ResourcePermit' })).toBeNull();
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('preserves request fields and visible selections when the namespace list updates', () => {
    const form = () => (
      <ResourcePermitSetupForm
        cluster="main"
        namespaces={[]}
        templateKind="GlobalResourcePermitTemplate"
        templateName="grafana"
      />
    );
    const { rerender } = render(form());
    fireEvent.change(screen.getByLabelText('ResourcePermit name prefix', { exact: false }), {
      target: { value: 'incident' },
    });
    mocks.namespaceList.mockReturnValue([visibleNamespaces(['solar-dev', 'solar-test']), null]);
    rerender(form());
    expect(screen.getByRole('combobox', { name: 'Namespace' }).textContent).toBe('solar-test');
    expect(
      (screen.getByLabelText('ResourcePermit name prefix', { exact: false }) as HTMLInputElement)
        .value
    ).toBe('incident');
    mocks.namespaceList.mockReturnValue([visibleNamespaces(['solar-dev']), null]);
    rerender(form());
    expect(screen.getByRole('combobox', { name: 'Namespace' }).textContent).toBe('solar-dev');
  });

  it('keeps readable global templates selectable without matching reported availability', () => {
    mocks.globalTemplateList.mockReturnValue([
      [
        { getName: () => 'grafana', status: { namespaces: ['unseen-namespace'] } },
        { getName: () => 'clusterrole-bindings', status: { namespaces: ['*'] } },
      ],
      null,
    ]);
    render(<CreateResourcePermit cluster="main" namespaces={['solar-test']} />);
    expect(screen.getByRole('button', { name: 'Select template grafana' })).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Select template clusterrole-bindings' })
    ).toBeTruthy();
  });

  it('filters the catalog through the labelled search input', () => {
    mocks.globalTemplateList.mockReturnValue([
      [{ getName: () => 'grafana' }, { getName: () => 'clusterrole-bindings' }],
      null,
    ]);
    render(<CreateResourcePermit cluster="main" namespaces={['solar-test']} />);

    fireEvent.change(screen.getByRole('textbox', { name: 'Search templates' }), {
      target: { value: 'grafana' },
    });

    expect(screen.getByRole('button', { name: 'Select template grafana' })).toBeTruthy();
    expect(
      screen.queryByRole('button', { name: 'Select template clusterrole-bindings' })
    ).toBeNull();
  });

  async function submitGeneratedRequest(startTime?: string, prefix = 'incident') {
    render(
      <ResourcePermitSetupForm
        cluster="main"
        namespaces={['solar-test']}
        templateKind="GlobalResourcePermitTemplate"
        templateName="access"
      />
    );
    expect(
      (screen.getByRole('checkbox', { name: 'Generate a unique request name' }) as HTMLInputElement)
        .checked
    ).toBe(true);
    expect((screen.getByLabelText('Reason') as HTMLInputElement).required).toBe(false);
    fireEvent.change(screen.getByLabelText('ResourcePermit name prefix', { exact: false }), {
      target: { value: prefix },
    });
    if (startTime) {
      const startTimeInput = screen.getByLabelText('Start time') as HTMLInputElement;
      expect(startTimeInput.type).toBe('datetime-local');
      expect(startTimeInput.required).toBe(false);
      expect(
        document.getElementById(startTimeInput.getAttribute('aria-describedby') || '')?.textContent
      ).toBe('Optional future activation time.');
      fireEvent.change(startTimeInput, { target: { value: startTime } });
    }
    fireEvent.click(screen.getByRole('button', { name: 'Template Parameters' }));
    const createButton = await screen.findByRole('button', { name: 'Create ResourcePermit' });
    await act(async () => fireEvent.click(createButton));
  }

  it('uses the template name as the generated prefix when the field is empty', async () => {
    mocks.post.mockResolvedValue({ metadata: { name: 'access-x7j2q', namespace: 'solar-test' } });

    await submitGeneratedRequest(undefined, '');

    expect(screen.queryByText('Required')).toBeNull();
    expect(mocks.post.mock.calls[0][1].metadata).toEqual({
      generateName: 'access-',
      namespace: 'solar-test',
    });
  });

  it('uses the template name as the default generated prefix value', () => {
    render(
      <ResourcePermitSetupForm
        cluster="main"
        namespaces={['solar-test']}
        templateKind="GlobalResourcePermitTemplate"
        templateName="access"
      />
    );

    const nameField = screen.getByLabelText('ResourcePermit name prefix', {
      exact: false,
    }) as HTMLInputElement;
    expect(nameField.value).toBe('access-');
    expect(screen.getByText('Kubernetes will append a unique suffix to access-.')).toBeTruthy();
    expect(nameField.required).toBe(false);
  });

  it('still requires a name when generated naming is disabled', () => {
    render(
      <ResourcePermitSetupForm
        cluster="main"
        namespaces={['solar-test']}
        templateKind="GlobalResourcePermitTemplate"
        templateName="access"
      />
    );

    fireEvent.click(screen.getByRole('checkbox', { name: 'Generate a unique request name' }));
    fireEvent.click(screen.getByRole('button', { name: 'Template Parameters' }));

    expect(screen.getByText('Required')).toBeTruthy();
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('creates without a reason and navigates to the generated object, closing only its wizard', async () => {
    mocks.post.mockResolvedValue({ metadata: { name: 'incident-x7j2q', namespace: 'solar-test' } });
    await submitGeneratedRequest();
    await waitFor(() =>
      expect(mocks.push).toHaveBeenCalledWith(
        '/c/main/customresources/resourcepermits.addons.projectcapsule.dev/solar-test/incident-x7j2q'
      )
    );
    expect(mocks.post.mock.calls[0][1].metadata).toEqual({
      generateName: 'incident-',
      namespace: 'solar-test',
    });
    expect(mocks.post.mock.calls[0][1].spec).not.toHaveProperty('reason');
    expect(mocks.close).toHaveBeenCalledWith('permits-create-resource-permit solar-test main');
  });

  it('keeps the wizard open after an API failure', async () => {
    mocks.post.mockRejectedValue(new Error('Request rejected'));
    await submitGeneratedRequest();
    expect(await screen.findByText('Request rejected')).toBeTruthy();
    expect(mocks.push).not.toHaveBeenCalled();
    expect(mocks.close).not.toHaveBeenCalled();
  });

  it('preserves a chosen activation time through the wizard and submits it', async () => {
    const startTime = '2099-01-02T03:04';
    mocks.post.mockResolvedValue({ metadata: { name: 'incident-x7j2q', namespace: 'solar-test' } });

    await submitGeneratedRequest(startTime);

    expect(mocks.post.mock.calls[0][1].spec.startTime).toBe(new Date(startTime).toISOString());
  });
});
