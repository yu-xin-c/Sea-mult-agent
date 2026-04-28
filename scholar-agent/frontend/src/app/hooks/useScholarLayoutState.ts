import { useEffect, useState } from 'react';

export function useScholarLayoutState() {
  const [leftPanelWidth, setLeftPanelWidth] = useState(35);
  const [isResizing, setIsResizing] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(450);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (isResizing) {
        const newWidth = (e.clientX / window.innerWidth) * 100;
        if (newWidth > 20 && newWidth < 80) {
          setLeftPanelWidth(newWidth);
        }
      } else if (isResizingSidebar) {
        const newSidebarWidth = window.innerWidth - e.clientX;
        if (newSidebarWidth > 300 && newSidebarWidth < window.innerWidth * 0.6) {
          setSidebarWidth(newSidebarWidth);
        }
      }
    };

    const handleMouseUp = () => {
      setIsResizing(false);
      setIsResizingSidebar(false);
      document.body.style.cursor = 'default';
    };

    if (isResizing || isResizingSidebar) {
      window.addEventListener('mousemove', handleMouseMove);
      window.addEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = 'col-resize';
    }

    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isResizing, isResizingSidebar]);

  return {
    leftPanelWidth,
    isResizing,
    sidebarWidth,
    isResizingSidebar,
    startResizingLeftPanel: () => setIsResizing(true),
    startResizingSidebar: () => setIsResizingSidebar(true),
  };
}
