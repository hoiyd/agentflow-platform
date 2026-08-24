export type LatestRequestLease = {
  signal: AbortSignal;
  isCurrent: () => boolean;
};

export type LatestRequestController = {
  begin: () => LatestRequestLease;
  cancel: () => void;
};

// A lease protects state owned by the latest navigation request. The revision
// check remains necessary when an underlying dependency ignores AbortSignal.
export function createLatestRequestController(): LatestRequestController {
  let revision = 0;
  let controller: AbortController | null = null;

  return {
    begin() {
      controller?.abort();
      controller = new AbortController();
      const requestRevision = ++revision;
      const signal = controller.signal;

      return {
        signal,
        isCurrent: () => requestRevision === revision && !signal.aborted
      };
    },
    cancel() {
      revision += 1;
      controller?.abort();
      controller = null;
    }
  };
}
