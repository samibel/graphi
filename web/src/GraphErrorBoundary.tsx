// Error boundary around the WebGL graph canvas. A Sigma render error (e.g. an
// unregistered node/edge program type or a WebGL context failure) previously
// unmounted the whole React tree — the user saw a blank white page with no
// message. The boundary contains the failure to the canvas area and shows the
// error text plus a retry button instead.
import { Component, type ReactNode } from "react";

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

export class GraphErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  render() {
    if (this.state.error) {
      return (
        <div className="error" role="alert">
          <p>⚠ The graph view crashed: {this.state.error.message}</p>
          <button type="button" onClick={() => this.setState({ error: null })}>
            try again
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
